package tflarrivals

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type LineTracker struct {
	Line        TfLLine
	TfLAppKey   string
	RefreshRate time.Duration
	Service     *ctdf.Service
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

	log.Info().
		Str("id", l.Line.LineID).
		Str("type", string(l.Line.TransportType)).
		Str("service", l.Service.PrimaryIdentifier).
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
			nameRegex := regexp.MustCompile("(.+) Underground Station")

			destinationDisplay := predictions[0].DestinationName
			if destinationDisplay == "" && predictions[0].Towards != "" && predictions[0].Towards != "Check Front of Train" {
				destinationDisplay = predictions[0].Towards
			} else if destinationDisplay == "" {
				destinationDisplay = l.Service.ServiceName
			}

			nameMatches := nameRegex.FindStringSubmatch(predictions[0].DestinationName)

			if len(nameMatches) == 2 {
				destinationDisplay = nameMatches[1]
			}

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

					DestinationDisplay: destinationDisplay,
				},

				Stops: map[string]*ctdf.RealtimeJourneyStops{},
			}
		}

		realtimeJourney.ModificationDateTime = now

		updatedStops := map[string]bool{}
		for _, prediction := range predictions {
			stopID := fmt.Sprintf("GB:TFL:STOP:%s", prediction.NaptanID)

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