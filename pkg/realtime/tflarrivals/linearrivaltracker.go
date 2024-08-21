package tflarrivals

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/sourcegraph/conc/pool"
	"golang.org/x/exp/slices"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type LineArrivalTracker struct {
	Line        *TfLLine
	RefreshRate time.Duration

	OrderedLineRoutes []OrderedLineRoute
}

func (l *LineArrivalTracker) Run() {
	if l.Line.Service == nil {
		log.Error().
			Str("id", l.Line.LineID).
			Str("type", string(l.Line.TransportType)).
			Msg("Failed setting up line tracker - couldnt find CTDF Service")
		return
	}

	l.GetTfLRouteSequences()
	if len(l.OrderedLineRoutes) == 0 {
		log.Error().
			Str("id", l.Line.LineID).
			Str("type", string(l.Line.TransportType)).
			Msg("Failed setting up line tracker - couldnt TfL ordered line routes")
		return
	}

	log.Info().
		Str("id", l.Line.LineID).
		Str("type", string(l.Line.TransportType)).
		Str("service", l.Line.Service.PrimaryIdentifier).
		Int("numroutes", len(l.OrderedLineRoutes)).
		Msg("Registering new line arrival tracker")

	for {
		startTime := time.Now()

		arrivals := l.GetLatestArrivals()
		l.ParseArrivals(arrivals)

		endTime := time.Now()
		executionDuration := endTime.Sub(startTime)
		waitTime := l.RefreshRate - executionDuration

		if waitTime.Seconds() > 0 {
			time.Sleep(waitTime)
		}
	}
}

func (l *LineArrivalTracker) GetTfLRouteSequences() {
	for _, direction := range []string{"inbound", "outbound"} {
		requestURL := fmt.Sprintf(
			"https://api.tfl.gov.uk/line/%s/route/sequence/%s/?app_key=%s",
			l.Line.LineID,
			direction,
			TfLAppKey)
		req, _ := http.NewRequest("GET", requestURL, nil)
		req.Header["user-agent"] = []string{"curl/7.54.1"} // TfL is protected by cloudflare and it gets angry when no user agent is set

		client := &http.Client{}
		resp, err := client.Do(req)

		if err != nil {
			log.Fatal().Err(err).Msg("Download file")
		}
		defer resp.Body.Close()

		jsonBytes, _ := io.ReadAll(resp.Body)

		var routeSequenceResponse RouteSequenceResponse
		json.Unmarshal(jsonBytes, &routeSequenceResponse)

		l.OrderedLineRoutes = append(l.OrderedLineRoutes, routeSequenceResponse.OrderedLineRoutes...)
	}
}

func (l *LineArrivalTracker) GetLatestArrivals() []ArrivalPrediction {
	requestURL := fmt.Sprintf("https://api.tfl.gov.uk/line/%s/arrivals?app_key=%s", l.Line.LineID, TfLAppKey)
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

	return lineArrivals
}

func (l *LineArrivalTracker) ParseArrivals(lineArrivals []ArrivalPrediction) {
	startTime := time.Now()

	datasource := &ctdf.DataSource{
		OriginalFormat: "tfl-json",
		Provider:       "GB-TfL",
		DatasetID:      fmt.Sprintf("gb-tfl-line/%s/arrivals", l.Line.LineID),
		Timestamp:      fmt.Sprint(startTime.Unix()),
	}

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	//var realtimeJourneyUpdateOperations []mongo.WriteModel
	p := pool.NewWithResults[mongo.WriteModel]()

	// Group all the arrivals predictions that are part of the same journey
	groupedLineArrivals := map[string][]ArrivalPrediction{}
	for _, arrival := range lineArrivals {
		realtimeJourneyID := fmt.Sprintf(
			"REALTIME:TFL:%s:%s:%s:%s:%s",
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
		realtimeJourneyID := realtimeJourneyID
		predictions := predictions

		p.Go(func() mongo.WriteModel {
			return l.parseGroupedArrivals(realtimeJourneyID, predictions, datasource)
		})
	}

	realtimeJourneyUpdateOperations := p.Wait()

	processingTime := time.Now().Sub(startTime)
	startTime = time.Now()

	if len(realtimeJourneyUpdateOperations) > 0 {
		_, err := realtimeJourneysCollection.BulkWrite(context.Background(), realtimeJourneyUpdateOperations, &options.BulkWriteOptions{})

		if err != nil {
			log.Fatal().Err(err).Msg("Failed to bulk write Realtime Journeys")
		}
	}

	log.Info().
		Str("id", l.Line.LineID).
		Str("processing", processingTime.String()).
		Str("bulkwrite", time.Now().Sub(startTime).String()).
		Int("length", len(realtimeJourneyUpdateOperations)).
		Msg("update line arrivals")

	// Remove any tfl realtime journey that wasnt updated in this run
	// This means its dropped off all the stop arrivals (most likely as its finished)
	deleteQuery := bson.M{
		"datasource.provider":  datasource.Provider,
		"datasource.datasetid": datasource.DatasetID,
		"datasource.timestamp": bson.M{"$ne": datasource.Timestamp},
	}

	d, _ := realtimeJourneysCollection.DeleteMany(context.Background(), deleteQuery)
	if d.DeletedCount > 0 {
		log.Info().
			Str("id", l.Line.LineID).
			Int64("length", d.DeletedCount).
			Msg("delete expired journeys")
	}
}

func (l *LineArrivalTracker) parseGroupedArrivals(realtimeJourneyID string, predictions []ArrivalPrediction, datasource *ctdf.DataSource) mongo.WriteModel {
	tflOperator := &ctdf.Operator{
		PrimaryIdentifier: "GB:NOC:TFLO",
		PrimaryName:       "Transport for London",
	}
	now := time.Now()

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	searchQuery := bson.M{"primaryidentifier": realtimeJourneyID}

	var realtimeJourney *ctdf.RealtimeJourney

	opts := options.FindOne().SetProjection(bson.D{
		{Key: "stops", Value: 1},
		{Key: "journey", Value: 1},
	})

	realtimeJourneysCollection.FindOne(context.Background(), searchQuery, opts).Decode(&realtimeJourney)

	newRealtimeJourney := false
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

				Service:    l.Line.Service,
				ServiceRef: l.Line.Service.PrimaryIdentifier,

				DepartureTimezone: "Europe/London",
			},
			Service:        l.Line.Service,
			JourneyRunDate: journeyDate,

			Stops: map[string]*ctdf.RealtimeJourneyStops{},
		}

		newRealtimeJourney = true
	}

	updateMap := bson.M{
		"modificationdatetime": now,
	}

	// Add new predictions to the realtime journey
	platformMatchRegex, _ := regexp.Compile("(\\w+) (?:- )?Platform (\\d+)")
	updatedStops := map[string]bool{}
	for _, prediction := range predictions {
		stopID := getStopFromTfLStop(prediction.NaptanID).PrimaryIdentifier

		scheduledTime, _ := time.Parse(time.RFC3339, prediction.ExpectedArrival)
		scheduledTime = scheduledTime.In(now.Location())

		platform := prediction.PlatformName
		platformMatches := platformMatchRegex.FindStringSubmatch(platform)
		if len(platformMatches) == 3 {
			platform = fmt.Sprintf("%s - %s", platformMatches[2], platformMatches[1])
		}

		realtimeJourney.Stops[stopID] = &ctdf.RealtimeJourneyStops{
			StopRef:       stopID,
			TimeType:      ctdf.RealtimeJourneyStopTimeEstimatedFuture,
			ArrivalTime:   scheduledTime,
			DepartureTime: scheduledTime,

			Platform: platform,
		}

		updatedStops[stopID] = true
	}

	// Iterate over stops in the realtime journey and any that arent included in this update get changed to historical
	for key, stop := range realtimeJourney.Stops {
		if !updatedStops[stop.StopRef] {
			stop.TimeType = ctdf.RealtimeJourneyStopTimeHistorical
		}

		if key != "" {
			updateMap[fmt.Sprintf("stops.%s", key)] = stop
		}
	}

	// Order the realtime journey stops by arrival time in ascending order
	var realtimeJourneyStops []*ctdf.RealtimeJourneyStops
	for _, stop := range realtimeJourney.Stops {
		realtimeJourneyStops = append(realtimeJourneyStops, stop)
	}
	sort.SliceStable(realtimeJourneyStops, func(a, b int) bool {
		aTime := realtimeJourneyStops[a].ArrivalTime
		bTime := realtimeJourneyStops[b].ArrivalTime

		return aTime.Before(bTime)
	})

	// Get all the naptan ids this realtime journey stops contains in order of arrival
	var journeyOrderedNaptanIDs []string
	for _, stop := range realtimeJourneyStops {
		journeyOrderedNaptanIDs = append(journeyOrderedNaptanIDs, stop.StopRef)
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
	for _, route := range l.OrderedLineRoutes {
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
			if slices.Contains[[]string](journeyOrderedNaptanIDs, tflFormattedNaptanID) {
				reducedRouteOrderedNaptanIDs = append(reducedRouteOrderedNaptanIDs, tflFormattedNaptanID)
			}
		}

		// If the slices equal then we have a potential match
		if slices.Equal[[]string](journeyOrderedNaptanIDs, reducedRouteOrderedNaptanIDs) {
			potentialOrderLineRouteMatches = append(potentialOrderLineRouteMatches, route)
		}
	}

	// Update destination display
	nameRegex := regexp.MustCompile("(.+) (Underground|DLR) Station")

	destinationDisplay := lastPrediction.DestinationName
	if destinationDisplay == "" && lastPrediction.Towards != "" && lastPrediction.Towards != "Check Front of Train" {
		destinationDisplay = lastPrediction.Towards
	} else if destinationDisplay == "" {
		destinationDisplay = l.Line.Service.ServiceName
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
		realtimeJourney.Journey.DestinationDisplay = fmt.Sprintf("[X-%d] %s", len(potentialOrderLineRouteMatches), realtimeJourney.Journey.DestinationDisplay)

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
		realtimeStop := realtimeJourney.Stops[item.OriginStopRef]

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
				updateMap["departedstopref"] = realtimeJourney.DepartedStopRef
				updateMap["departedstop"] = realtimeJourney.DepartedStop

				realtimeJourney.NextStopRef = referenceItem.DestinationStopRef
				realtimeJourney.NextStop = referenceItem.DestinationStop
				updateMap["nextstopref"] = realtimeJourney.NextStopRef
				updateMap["nextstop"] = realtimeJourney.NextStop
			}
		}
	}

	// Update database
	if newRealtimeJourney {
		updateMap["primaryidentifier"] = realtimeJourney.PrimaryIdentifier
		updateMap["activelytracked"] = realtimeJourney.ActivelyTracked
		updateMap["timeoutdurationminutes"] = realtimeJourney.TimeoutDurationMinutes

		updateMap["reliability"] = realtimeJourney.Reliability

		updateMap["creationdatetime"] = realtimeJourney.CreationDateTime
		updateMap["datasource"] = realtimeJourney.DataSource

		updateMap["vehicleref"] = realtimeJourney.VehicleRef

		updateMap["service"] = realtimeJourney.Service
		updateMap["journeyrundate"] = realtimeJourney.JourneyRunDate
	} else {
		updateMap["datasource.timestamp"] = datasource.Timestamp
	}
	updateMap["journey"] = realtimeJourney.Journey

	updateMap["vehiclelocationdescription"] = realtimeJourney.VehicleLocationDescription

	// Create update
	bsonRep, _ := bson.Marshal(bson.M{"$set": updateMap})
	updateModel := mongo.NewUpdateOneModel()
	updateModel.SetFilter(searchQuery)
	updateModel.SetUpdate(bsonRep)
	updateModel.SetUpsert(true)

	return updateModel
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

	var stopGroup *ctdf.StopGroup
	stopGroupCollection.FindOne(context.Background(), bson.M{"otheridentifiers.AtcoCode": tflStopID}).Decode(&stopGroup)

	if stopGroup == nil {
		var stop *ctdf.Stop
		stopCollection.FindOne(context.Background(), bson.M{"platforms.primaryidentifier": fmt.Sprintf("GB:ATCO:%s", tflStopID)}).Decode(&stop)

		if stop == nil {
			return nil
		}

		stopTflStopCacheMutex.Lock()
		stopTflStopCache[tflStopID] = stop
		stopTflStopCacheMutex.Unlock()

		return stop
	}

	var stop *ctdf.Stop
	stopCollection.FindOne(context.Background(), bson.M{"associations.associatedidentifier": stopGroup.PrimaryIdentifier}).Decode(&stop)

	stopTflStopCacheMutex.Lock()
	stopTflStopCache[tflStopID] = stop
	stopTflStopCacheMutex.Unlock()

	return stop
}
