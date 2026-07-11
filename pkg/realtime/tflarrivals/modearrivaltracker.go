package tflarrivals

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/sourcegraph/conc/pool"
	"golang.org/x/exp/slices"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"go.mongodb.org/mongo-driver/bson"
)

type ModeArrivalTracker struct {
	Mode        *TfLMode
	RefreshRate time.Duration

	RuntimeJourneyFilter         RuntimeJourneyFilter
	RuntimeJourneyFilterProvider RuntimeJourneyFilterProvider
}

type RuntimeJourneyFilter func(string, string) bool
type RuntimeJourneyFilterProvider func(context.Context) (RuntimeJourneyFilter, error)

const modeArrivalMaxConcurrentJourneys = 32

func (l *ModeArrivalTracker) Run(getRoutes bool) {
	// if l.Line.Service == nil {
	// 	log.Error().
	// 		Str("id", l.Line.LineID).
	// 		Str("type", string(l.Line.TransportType)).
	// 		Msg("Failed setting up line tracker - couldnt find CTDF Service")
	// 	return
	// }

	if getRoutes {
		for _, line := range l.Mode.Lines {
			line.GetTfLRouteSequences()
			if len(line.OrderedLineRoutes) == 0 {
				log.Error().
					Str("id", line.LineID).
					Str("type", string(line.TransportType)).
					Msg("Failed setting up line tracker - couldnt TfL ordered line routes")
				// return
			}
		}
	}

	log.Info().
		Str("id", l.Mode.ModeID).
		Str("type", string(l.Mode.TransportType)).
		Int("numlines", len(l.Mode.Lines)).
		Msg("Registering new mode arrival tracker")

	for {
		startTime := time.Now()

		arrivals := l.GetLatestArrivals()
		l.ParseArrivals(arrivals)

		endTime := time.Now()
		executionDuration := endTime.Sub(startTime)
		waitTime := (l.RefreshRate - executionDuration) + (time.Duration(rand.IntN(3)) * time.Second)

		if waitTime.Seconds() > 0 {
			time.Sleep(waitTime)
		}
	}
}

func (l *ModeArrivalTracker) GetLatestArrivals() []ArrivalPrediction {
	requestURL := modeArrivalsURL(l.Mode.ModeID, TfLAppKey)
	req, _ := http.NewRequest("GET", requestURL, nil)
	req.Header["user-agent"] = []string{"curl/7.54.1"} // TfL is protected by cloudflare and it gets angry when no user agent is set

	client := &http.Client{}
	resp, err := client.Do(req)

	if err != nil {
		log.Fatal().Err(err).Msg("Download file")
	}
	defer resp.Body.Close()

	jsonBytes, _ := io.ReadAll(resp.Body)

	var lineArrivals []ArrivalPrediction
	json.Unmarshal(jsonBytes, &lineArrivals)
	if l.Mode.ModeID == "bus" {
		cacheBusArrivals(lineArrivals)
	}

	return lineArrivals
}

func modeArrivalsURL(modeID string, appKey string) string {
	query := url.Values{}
	query.Set("count", modeArrivalsCount(modeID))
	query.Set("app_key", appKey)

	return fmt.Sprintf("https://api.tfl.gov.uk/mode/%s/arrivals?%s", url.PathEscape(modeID), query.Encode())
}

func modeArrivalsCount(modeID string) string {
	// The bus mode has far more stops than the other TfL modes. Retain enough
	// predictions for a useful board while keeping the frequent bus poll small.
	if modeID == "bus" {
		return "8"
	}

	return "-1"
}

func (l *ModeArrivalTracker) ParseArrivals(lineArrivals []ArrivalPrediction) {
	startTime := time.Now()
	runtimeJourneyFilter := l.RuntimeJourneyFilter
	if l.RuntimeJourneyFilterProvider != nil {
		var err error
		runtimeJourneyFilter, err = l.RuntimeJourneyFilterProvider(context.Background())
		if err != nil {
			log.Error().Err(err).Str("id", l.Mode.ModeID).Msg("Failed to load runtime journey filter")
			return
		}
	}
	if runtimeJourneyFilter == nil {
		runtimeJourneyFilter = func(string, string) bool { return true }
	}

	datasource := &ctdf.DataSourceReference{
		OriginalFormat: "tfl-json",
		ProviderName:   "Transport for London",
		ProviderID:     "gb-tfl",
		DatasetID:      fmt.Sprintf("gb-tfl-mode/%s/arrivals", l.Mode.ModeID),
		Timestamp:      fmt.Sprint(startTime.Unix()),
	}

	p := pool.NewWithResults[struct{}]().WithMaxGoroutines(modeArrivalMaxConcurrentJourneys)

	// Group all the arrivals predictions that are part of the same journey
	groupedLineArrivals := map[string][]ArrivalPrediction{}
	for _, arrival := range lineArrivals {
		realtimeJourneyID := fmt.Sprintf(
			"realtime-tfl-%s-%s-%s-%s-%s",
			arrival.ModeName,
			arrival.LineID,
			arrival.Direction,
			arrival.VehicleID,
			arrival.DestinationNaptanID,
		)

		groupedLineArrivals[realtimeJourneyID] = append(groupedLineArrivals[realtimeJourneyID], arrival)
	}

	// Generate RealtimeJourneys for each group
	for realtimeJourneyID, predictions := range groupedLineArrivals {
		if runtimeJourneyFilter(predictions[0].LineID, predictions[0].TripID) {
			realtimeJourneyID := realtimeJourneyID
			predictions := predictions

			p.Go(func() struct{} {
				err := l.parseGroupedArrivals(realtimeJourneyID, predictions, datasource)
				if err != nil {
					log.Error().Err(err).Msg("Failed to parse grouped arrivals")
				}
				return struct{}{}
			})
		}
	}

	realtimeJourneyUpdateOperations := p.Wait()

	processingTime := time.Since(startTime)
	startTime = time.Now()

	log.Info().
		Str("id", l.Mode.ModeID).
		Str("processing", processingTime.String()).
		Str("bulkwrite", time.Since(startTime).String()).
		Int("length", len(realtimeJourneyUpdateOperations)).
		Msg("update mode arrivals")

	// Remove any tfl realtime journey that wasnt updated in this run
	// This means its dropped off all the stop arrivals (most likely as its finished)
	// TODO NEED THIS LOGIC BACK IN REDIS
	// deleteQuery := bson.M{
	// 	"datasource.provider":  datasource.ProviderName,
	// 	"datasource.datasetid": datasource.DatasetID,
	// 	"datasource.timestamp": bson.M{"$ne": datasource.Timestamp},
	// }

	// d, _ := realtimeJourneysCollection.DeleteMany(context.Background(), deleteQuery)
	// if d.DeletedCount > 0 {
	// 	log.Info().
	// 		Str("id", l.Mode.ModeID).
	// 		Int64("length", d.DeletedCount).
	// 		Msg("delete expired journeys")
	// }
}

func (l *ModeArrivalTracker) parseGroupedArrivals(realtimeJourneyID string, predictions []ArrivalPrediction, datasource *ctdf.DataSourceReference) error {
	tflOperator := &ctdf.Operator{
		PrimaryIdentifier: "gb-noc-TFLO",
		PrimaryName:       "Transport for London",
	}
	now := time.Now()

	line := l.Mode.Lines[predictions[0].LineID]

	// Skip ones with a nil service
	if line.Service == nil {
		return nil
	}

	realtimeJourney, _ := realtimestore.FindByIdentifier(context.Background(), realtimeJourneyID)

	if realtimeJourney == nil {
		journeyDate := time.Now() // TODO may not always be correct?

		realtimeJourney = &ctdf.RealtimeJourney{
			PrimaryIdentifier:      realtimeJourneyID,
			TimeoutDurationMinutes: 10,
			ActivelyTracked:        true,
			CreationDateTime:       now,
			VehicleRef:             predictions[0].VehicleID,
			Reliability:            ctdf.RealtimeJourneyReliabilityExternalProvided,

			DataSource: datasource,

			Journey: &ctdf.Journey{
				PrimaryIdentifier: realtimeJourneyID,

				Operator:    tflOperator,
				OperatorRef: tflOperator.PrimaryIdentifier,

				Service:    line.Service,
				ServiceRef: line.Service.PrimaryIdentifier,

				DepartureTimezone: "Europe/London",
			},
			Service:        line.Service,
			JourneyRunDate: journeyDate,

			Stops: map[string]*ctdf.RealtimeJourneyStops{},
		}
	}

	// Add new predictions to the realtime journey
	platformMatchRegex, _ := regexp.Compile(`(\w+) (?:- )?Platform (\d+)`)
	previousStops := realtimeJourney.Stops
	currentStops := make([]*ctdf.RealtimeJourneyStops, 0, len(predictions))
	for _, prediction := range predictions {
		stop := getStopFromTfLStop(prediction.NaptanID)

		if stop == nil {
			continue
		}

		stopID := stop.PrimaryIdentifier

		scheduledTime, _ := time.Parse(time.RFC3339, prediction.ExpectedArrival)

		stopTimezone, _ := time.LoadLocation(stop.Timezone)
		scheduledTime = scheduledTime.In(stopTimezone)

		platform := prediction.PlatformName
		platformMatches := platformMatchRegex.FindStringSubmatch(platform)
		if len(platformMatches) == 3 {
			platform = fmt.Sprintf("%s - %s", platformMatches[2], platformMatches[1])
		}
		if platform == "null" {
			platform = ""
		}

		currentStops = append(currentStops, &ctdf.RealtimeJourneyStops{
			StopRef:       stopID,
			TimeType:      ctdf.RealtimeJourneyStopTimeEstimatedFuture,
			ArrivalTime:   scheduledTime,
			DepartureTime: scheduledTime,

			Platform: platform,
		})
	}

	// TfL revises the same prediction on every poll. Reconcile those revisions
	// with existing calls instead of archiving every ETA as a new stop.
	realtimeJourneyStops := reconcileTFLRealtimeStops(previousStops, currentStops, line.StopOccurrenceLimits)
	realtimeJourney.Stops = make(map[string]*ctdf.RealtimeJourneyStops, len(realtimeJourneyStops))
	for index, stop := range realtimeJourneyStops {
		stop.JourneyStopIndex = index
		realtimeJourney.SetRealtimeStop(stop)
	}

	// Get all the naptan ids this realtime journey stops contains in order of arrival
	var journeyOrderedNaptanIDs []string
	for _, stop := range realtimeJourneyStops {
		journeyOrderedNaptanIDs = append(journeyOrderedNaptanIDs, stop.StopRef)
	}

	if len(journeyOrderedNaptanIDs) == 0 {
		return nil
	}
	lastPredictionStop := journeyOrderedNaptanIDs[len(journeyOrderedNaptanIDs)-1]
	lastPrediction := predictions[len(predictions)-1]

	lastPredictionDestination := lastPrediction.DestinationNaptanID
	if lastPredictionDestination != "" {
		lastPredictionDestination = getStopFromTfLStop(lastPrediction.DestinationNaptanID).PrimaryIdentifier
	}
	if lastPredictionStop != lastPredictionDestination && lastPredictionDestination != "" {
		journeyOrderedNaptanIDs = append(journeyOrderedNaptanIDs, lastPredictionDestination)
	}

	realtimeJourney.VehicleLocationDescription = lastPrediction.CurrentLocation

	// Identify the route the vehicle is on
	var potentialOrderLineRouteMatches []OrderedLineRoute
	for _, route := range line.OrderedLineRoutes {
		routeOrderedNaptanIDs := route.NaptanIDs

		// Reduce the route naptan ids so that it only contains the same stops in the predictions
		var reducedRouteOrderedNaptanIDs []string
		for _, naptanID := range routeOrderedNaptanIDs {
			stop := getStopFromTfLStop(naptanID)
			if stop == nil {
				log.Error().Str("naptanid", naptanID).Msg("Could not find stop for tfl stop")
				continue
			}
			tflFormattedNaptanID := stop.PrimaryIdentifier
			if slices.Contains(journeyOrderedNaptanIDs, tflFormattedNaptanID) {
				reducedRouteOrderedNaptanIDs = append(reducedRouteOrderedNaptanIDs, tflFormattedNaptanID)
			}
		}

		// If the slices equal then we have a potential match
		if slices.Equal(journeyOrderedNaptanIDs, reducedRouteOrderedNaptanIDs) {
			potentialOrderLineRouteMatches = append(potentialOrderLineRouteMatches, route)
		}
	}

	// Update destination display
	nameRegex := regexp.MustCompile("(.+) (Underground|DLR) Station")

	destinationDisplay := lastPrediction.DestinationName
	if destinationDisplay == "" && lastPrediction.Towards != "" && lastPrediction.Towards != "Check Front of Train" {
		destinationDisplay = lastPrediction.Towards
	} else if destinationDisplay == "" {
		destinationDisplay = line.Service.ServiceName
	}

	nameMatches := nameRegex.FindStringSubmatch(lastPrediction.DestinationName)
	if len(nameMatches) == 2 {
		destinationDisplay = nameMatches[1]
	}
	realtimeJourney.Journey.DestinationDisplay = destinationDisplay

	// Work out the route the vehicle is on
	var vehicleJourneyPath []*ctdf.JourneyPathItem
	if len(potentialOrderLineRouteMatches) == 1 {
		for i := 1; i < len(potentialOrderLineRouteMatches[0].NaptanIDs); i++ {
			originTfLID := potentialOrderLineRouteMatches[0].NaptanIDs[i-1]
			originStop := getStopFromTfLStop(originTfLID)
			if originStop == nil {
				log.Error().Str("naptanid", originTfLID).Msg("Could not find stop for tfl stop")
				continue
			}

			destinationTfLID := potentialOrderLineRouteMatches[0].NaptanIDs[i]
			destinationStop := getStopFromTfLStop(destinationTfLID)
			if destinationStop == nil {
				log.Error().Str("naptanid", destinationTfLID).Msg("Could not find stop for tfl stop")
				continue
			}

			vehicleJourneyPath = append(vehicleJourneyPath, &ctdf.JourneyPathItem{
				OriginStop:    originStop,
				OriginStopRef: originStop.PrimaryIdentifier,

				DestinationStop:    destinationStop,
				DestinationStopRef: destinationStop.PrimaryIdentifier,
			})
		}
	} else {
		log.Debug().
			Str("id", realtimeJourneyID).
			Int("route_matches", len(potentialOrderLineRouteMatches)).
			Msg("Using prediction-derived TfL journey path")

		for i := 1; i < len(journeyOrderedNaptanIDs); i++ {
			originStop := journeyOrderedNaptanIDs[i-1]
			destinationStop := journeyOrderedNaptanIDs[i]

			vehicleJourneyPath = append(vehicleJourneyPath, &ctdf.JourneyPathItem{
				OriginStopRef: originStop,

				DestinationStopRef: destinationStop,
			})
		}
	}
	realtimeJourney.Journey.Path = vehicleJourneyPath

	// Work out what the departed & next stops are
	// We find the first stop with an estimate instead of historical value and then base the departed/origin stops
	// on the journey path item right before that one
	// Also set the stop arrival times to be the same as the estimates
	realtimeJourney.DepartedStopRef = ""
	for i, item := range vehicleJourneyPath {
		realtimeStop := realtimeJourney.RealtimeStop(item.OriginStopRef, i)

		var referenceItem *ctdf.JourneyPathItem
		if i == 0 {
			referenceItem = vehicleJourneyPath[0]
		} else {
			referenceItem = vehicleJourneyPath[i-1]
		}

		if realtimeStop != nil {
			item.OriginArrivalTime = realtimeStop.ArrivalTime
			item.OriginDepartureTime = realtimeStop.DepartureTime

			if realtimeJourney.DepartedStopRef == "" && realtimeStop.TimeType == ctdf.RealtimeJourneyStopTimeEstimatedFuture {
				realtimeJourney.DepartedStopRef = referenceItem.OriginStopRef
				realtimeJourney.DepartedStop = referenceItem.OriginStop

				realtimeJourney.NextStopRef = referenceItem.DestinationStopRef
				realtimeJourney.NextStop = referenceItem.DestinationStop
			}
		}
	}

	// Update database
	realtimeJourney.ModificationDateTime = now
	realtimeJourney.DataSource.Timestamp = datasource.Timestamp

	if err := realtimestore.UpdateLocationDescription(context.Background(), realtimeJourney.PrimaryIdentifier, realtimeJourney.VehicleLocationDescription); err != nil {
		return err
	}
	if err := realtimestore.SaveRealtimeJourney(context.Background(), realtimeJourney); err != nil {
		return err
	}

	return realtimestore.IndexTFLDepartureBoardJourney(context.Background(), realtimeJourney)
}

func reconcileTFLRealtimeStops(previousStops map[string]*ctdf.RealtimeJourneyStops, currentStops []*ctdf.RealtimeJourneyStops, occurrenceLimits map[string]int) []*ctdf.RealtimeJourneyStops {
	sortRealtimeStops(currentStops)

	// Keep at most the number of occurrences that can exist on a real route.
	// This also repairs journeys polluted by the previous snapshot-accumulation
	// behaviour as soon as they receive another update.
	currentCounts := map[string]int{}
	filteredCurrentStops := make([]*ctdf.RealtimeJourneyStops, 0, len(currentStops))
	for _, stop := range currentStops {
		if stop == nil || stop.StopRef == "" {
			continue
		}
		if currentCounts[stop.StopRef] >= tflStopOccurrenceLimit(stop.StopRef, occurrenceLimits) {
			continue
		}
		currentCounts[stop.StopRef]++
		filteredCurrentStops = append(filteredCurrentStops, stop)
	}
	currentStops = filteredCurrentStops

	var earliestCurrent time.Time
	for _, stop := range currentStops {
		if earliestCurrent.IsZero() || stop.ArrivalTime.Before(earliestCurrent) {
			earliestCurrent = stop.ArrivalTime
		}
	}

	previous := make([]*ctdf.RealtimeJourneyStops, 0, len(previousStops))
	for _, stop := range previousStops {
		if stop != nil && stop.StopRef != "" {
			previous = append(previous, stop)
		}
	}
	sortRealtimeStops(previous)

	remainingCurrentMatches := make(map[string]int, len(currentCounts))
	for stopRef, count := range currentCounts {
		remainingCurrentMatches[stopRef] = count
	}
	historicalCounts := map[string]int{}
	result := make([]*ctdf.RealtimeJourneyStops, 0, len(previous)+len(currentStops))

	appendHistorical := func(stop *ctdf.RealtimeJourneyStops) {
		allowedHistorical := tflStopOccurrenceLimit(stop.StopRef, occurrenceLimits) - currentCounts[stop.StopRef]
		if allowedHistorical <= 0 || historicalCounts[stop.StopRef] >= allowedHistorical {
			return
		}
		stop.TimeType = ctdf.RealtimeJourneyStopTimeHistorical
		historicalCounts[stop.StopRef]++
		result = append(result, stop)
	}

	for _, stop := range previous {
		if stop.TimeType == ctdf.RealtimeJourneyStopTimeHistorical {
			appendHistorical(stop)
		}
	}
	for _, stop := range previous {
		if stop.TimeType != ctdf.RealtimeJourneyStopTimeEstimatedFuture {
			continue
		}
		if remainingCurrentMatches[stop.StopRef] > 0 {
			// The current feed contains this occurrence, so this is an ETA
			// revision and the new prediction replaces the old one.
			remainingCurrentMatches[stop.StopRef]--
			continue
		}
		if !earliestCurrent.IsZero() && stop.ArrivalTime.Before(earliestCurrent) {
			appendHistorical(stop)
		}
	}

	result = append(result, currentStops...)
	sortRealtimeStops(result)
	return result
}

func tflRouteStopOccurrenceLimits(routes []OrderedLineRoute) map[string]int {
	limits := map[string]int{}
	for _, route := range routes {
		routeCounts := map[string]int{}
		for _, naptanID := range route.NaptanIDs {
			stop := getStopFromTfLStop(naptanID)
			if stop == nil {
				continue
			}
			routeCounts[stop.PrimaryIdentifier]++
			if routeCounts[stop.PrimaryIdentifier] > limits[stop.PrimaryIdentifier] {
				limits[stop.PrimaryIdentifier] = routeCounts[stop.PrimaryIdentifier]
			}
		}
	}
	return limits
}

func tflStopOccurrenceLimit(stopRef string, occurrenceLimits map[string]int) int {
	if limit := occurrenceLimits[stopRef]; limit > 0 {
		return limit
	}
	return 1
}

func sortRealtimeStops(stops []*ctdf.RealtimeJourneyStops) {
	sort.SliceStable(stops, func(a, b int) bool {
		if stops[a].ArrivalTime.Equal(stops[b].ArrivalTime) {
			return stops[a].JourneyStopIndex < stops[b].JourneyStopIndex
		}
		return stops[a].ArrivalTime.Before(stops[b].ArrivalTime)
	})
}

// TODO convert to proper cache
var stopTflStopCacheMutex sync.Mutex
var stopTflStopCache map[string]*ctdf.Stop

func getStopFromTfLStop(tflStopID string) *ctdf.Stop {
	stopTflStopCacheMutex.Lock()
	cacheValue := stopTflStopCache[tflStopID]
	stopTflStopCacheMutex.Unlock()

	if cacheValue != nil {
		return cacheValue
	}

	stopGroupCollection := database.GetCollection("stop_groups")
	stopCollection := database.GetCollection("stops")

	var stop *ctdf.Stop
	stopCollection.FindOne(context.Background(), bson.M{"primaryidentifier": fmt.Sprintf("gb-atco-%s", tflStopID)}).Decode(&stop)

	if stop != nil {
		return stop
	}

	var stopGroup *ctdf.StopGroup
	stopGroupCollection.FindOne(context.Background(), bson.M{"otheridentifiers": fmt.Sprintf("gb-atco-%s", tflStopID)}).Decode(&stopGroup)

	if stopGroup == nil {
		var stop *ctdf.Stop
		stopCollection.FindOne(context.Background(), bson.M{"otheridentifiers": fmt.Sprintf("gb-atco-%s", tflStopID)}).Decode(&stop)

		if stop == nil {
			return nil
		}

		stopTflStopCacheMutex.Lock()
		stopTflStopCache[tflStopID] = stop
		stopTflStopCacheMutex.Unlock()

		return stop
	}

	stopCollection.FindOne(context.Background(), bson.M{"associations.associatedidentifier": stopGroup.PrimaryIdentifier}).Decode(&stop)

	stopTflStopCacheMutex.Lock()
	stopTflStopCache[tflStopID] = stop
	stopTflStopCacheMutex.Unlock()

	return stop
}
