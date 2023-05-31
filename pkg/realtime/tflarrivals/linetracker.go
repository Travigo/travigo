package tflarrivals

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/kr/pretty"
	"golang.org/x/exp/slices"
	"io"
	"net/http"
	"regexp"
	"sort"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const tflStopFormat = "GB:TFL:STOP:%s"

type LineTracker struct {
	Line        TfLLine
	TfLAppKey   string
	RefreshRate time.Duration
	Service     *ctdf.Service

	OrderedLineRoutes []OrderedLineRoute
}

func (l *LineTracker) Run() {
	l.GetService()
	if l.Service == nil {
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
		Str("service", l.Service.PrimaryIdentifier).
		Int("numroutes", len(l.OrderedLineRoutes)).
		Msg("Registering new line tracker")

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

func (l *LineTracker) GetService() {
	collection := database.GetCollection("services")

	query := bson.M{
		"operatorref":   "GB:NOC:TFLO",
		"servicename":   l.Line.LineName,
		"transporttype": l.Line.TransportType,
	}

	collection.FindOne(context.Background(), query).Decode(&l.Service)
}

func (l *LineTracker) GetTfLRouteSequences() {
	for _, direction := range []string{"inbound", "outbound"} {
		requestURL := fmt.Sprintf(
			"https://api.tfl.gov.uk/line/%s/route/sequence/%s/?app_key=%s",
			l.Line.LineID,
			direction,
			l.TfLAppKey)
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

func (l *LineTracker) GetLatestArrivals() []ArrivalPrediction {
	requestURL := fmt.Sprintf("https://api.tfl.gov.uk/line/%s/arrivals?app_key=%s", l.Line.LineID, l.TfLAppKey)
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

func (l *LineTracker) ParseArrivals(lineArrivals []ArrivalPrediction) {
	tflOperator := &ctdf.Operator{
		PrimaryIdentifier: "GB:NOC:TFLO",
		PrimaryName:       "Transport for London",
	}

	now := time.Now()
	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	var realtimeJourneyUpdateOperations []mongo.WriteModel

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
		searchQuery := bson.M{"primaryidentifier": realtimeJourneyID}

		var realtimeJourney *ctdf.RealtimeJourney

		realtimeJourneysCollection.FindOne(context.Background(), searchQuery).Decode(&realtimeJourney)

		if realtimeJourney == nil {
			realtimeJourney = &ctdf.RealtimeJourney{
				PrimaryIdentifier: realtimeJourneyID,
				CreationDateTime:  now,
				VehicleRef:        predictions[0].VehicleID,
				Reliability:       ctdf.RealtimeJourneyReliabilityExternalProvided,

				DataSource: &ctdf.DataSource{
					OriginalFormat: "tfl-json",
					Provider:       "GB-TfL",
					Dataset:        fmt.Sprintf("line/%s/arrivals", l.Line.LineID),
					Identifier:     fmt.Sprint(now.Unix()),
				},

				Journey: &ctdf.Journey{
					PrimaryIdentifier: realtimeJourneyID,

					Operator:    tflOperator,
					OperatorRef: tflOperator.PrimaryIdentifier,

					Service:    l.Service,
					ServiceRef: l.Service.PrimaryIdentifier,
				},

				Stops: map[string]*ctdf.RealtimeJourneyStops{},
			}
		}

		realtimeJourney.ModificationDateTime = now

		// Add new predictions to the realtime journey
		updatedStops := map[string]bool{}
		for _, prediction := range predictions {
			stopID := getStopFromTfLStop(prediction.NaptanID).PrimaryIdentifier

			scheduledTime, _ := time.Parse(time.RFC3339, prediction.ExpectedArrival)
			scheduledTime = scheduledTime.In(now.Location())

			realtimeJourney.Stops[stopID] = &ctdf.RealtimeJourneyStops{
				StopRef:       stopID,
				TimeType:      ctdf.RealtimeJourneyStopTimeEstimatedFuture,
				ArrivalTime:   scheduledTime,
				DepartureTime: scheduledTime,
			}

			updatedStops[stopID] = true
		}

		// Iterate over stops in the realtime journey and any that arent included in this update get changed to historical
		for _, stop := range realtimeJourney.Stops {
			if !updatedStops[stop.StopRef] {
				stop.TimeType = ctdf.RealtimeJourneyStopTimeHistorical
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
		lastPredictionDestination := fmt.Sprintf(tflStopFormat, lastPrediction.DestinationNaptanID)
		if lastPredictionStop != lastPredictionDestination && lastPredictionDestination != fmt.Sprintf(tflStopFormat, "") {
			journeyOrderedNaptanIDs = append(journeyOrderedNaptanIDs, lastPredictionDestination)
		}

		testJourneyID := "REALTIME:TFL:tube:piccadilly:outbound:325:940GZZLUCKS" // TODO DELETE THIS STUFF
		if realtimeJourney.PrimaryIdentifier == testJourneyID {
			pretty.Println("journey", journeyOrderedNaptanIDs)
		}

		// Identify the route the vehicle is on
		var potentialOrderLineRouteMatches []OrderedLineRoute
		for _, route := range l.OrderedLineRoutes {
			routeOrderedNaptanIDs := route.NaptanIDs

			// Reduce the route naptan ids so that it only contains the same stops in the predictions
			var reducedRouteOrderedNaptanIDs []string
			for _, naptanID := range routeOrderedNaptanIDs {
				//tflFormattedNaptanID := fmt.Sprintf(tflStopFormat, naptanID)
				tflFormattedNaptanID := getStopFromTfLStop(naptanID).PrimaryIdentifier
				if slices.Contains[string](journeyOrderedNaptanIDs, tflFormattedNaptanID) {
					reducedRouteOrderedNaptanIDs = append(reducedRouteOrderedNaptanIDs, tflFormattedNaptanID)
				}
			}

			// If the slices equal then we have a potential match
			if slices.Equal[string](journeyOrderedNaptanIDs, reducedRouteOrderedNaptanIDs) {
				potentialOrderLineRouteMatches = append(potentialOrderLineRouteMatches, route)
			}

			if realtimeJourney.PrimaryIdentifier == testJourneyID {
				pretty.Println("route", reducedRouteOrderedNaptanIDs)
			}
		}

		if realtimeJourney.PrimaryIdentifier == testJourneyID {
			pretty.Println(len(potentialOrderLineRouteMatches))
		}

		// Update destination display
		nameRegex := regexp.MustCompile("(.+) Underground Station")

		destinationDisplay := lastPrediction.DestinationName
		if destinationDisplay == "" && lastPrediction.Towards != "" && lastPrediction.Towards != "Check Front of Train" {
			destinationDisplay = lastPrediction.Towards
		} else if destinationDisplay == "" {
			destinationDisplay = l.Service.ServiceName
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

				destinationTfLID := potentialOrderLineRouteMatches[0].NaptanIDs[i]
				destinationStop := getStopFromTfLStop(destinationTfLID)

				vehicleJourneyPath = append(vehicleJourneyPath, &ctdf.JourneyPathItem{
					OriginStop:    originStop,
					OriginStopRef: originStop.PrimaryIdentifier,

					DestinationStop:    destinationStop,
					DestinationStopRef: destinationStop.PrimaryIdentifier,
				})
			}
		} else {
			realtimeJourney.Journey.DestinationDisplay = fmt.Sprintf("[X-%d] %s", len(potentialOrderLineRouteMatches), realtimeJourney.Journey.DestinationDisplay)
		}
		realtimeJourney.Journey.Path = vehicleJourneyPath

		// Create update
		bsonRep, _ := bson.Marshal(bson.M{"$set": realtimeJourney})
		updateModel := mongo.NewUpdateOneModel()
		updateModel.SetFilter(searchQuery)
		updateModel.SetUpdate(bsonRep)
		updateModel.SetUpsert(true)

		realtimeJourneyUpdateOperations = append(realtimeJourneyUpdateOperations, updateModel)
	}

	if len(realtimeJourneyUpdateOperations) > 0 {
		_, err := realtimeJourneysCollection.BulkWrite(context.TODO(), realtimeJourneyUpdateOperations, &options.BulkWriteOptions{})

		if err != nil {
			log.Fatal().Err(err).Msg("Failed to bulk write Realtime Journeys")
		}
	}
}

// TODO convert to proper cache
var stopTflStopCache map[string]*ctdf.Stop

func getStopFromTfLStop(tflStopID string) *ctdf.Stop {
	cacheValue := stopTflStopCache[tflStopID]
	if cacheValue != nil {
		return cacheValue
	}

	stopGroupCollection := database.GetCollection("stop_groups")
	var stopGroup *ctdf.StopGroup
	stopGroupCollection.FindOne(context.Background(), bson.M{"otheridentifiers.AtcoCode": tflStopID}).Decode(&stopGroup)

	if stopGroup == nil {
		return nil
	}

	stopCollection := database.GetCollection("stops")
	var stop *ctdf.Stop
	stopCollection.FindOne(context.Background(), bson.M{"associations.associatedidentifier": stopGroup.PrimaryIdentifier}).Decode(&stop)

	stopTflStopCache[tflStopID] = stop

	return stop
}
