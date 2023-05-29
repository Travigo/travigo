package tflarrivals

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	RefreshRate time.Duration
}

func (l LineTracker) Run() {
	log.Info().Str("lineid", l.Line.LineID).Str("transporttype", string(l.Line.TransportType)).Msg("Registering new line")

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

func (l LineTracker) GetLatestArrivals() []ArrivalPrediction {
	requestURL := fmt.Sprintf("https://api.tfl.gov.uk/line/%s/arrivals", l.Line.LineID)
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

func (l LineTracker) ParseArrivals(lineArrivals []ArrivalPrediction) {
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

				Stops: map[string]*ctdf.RealtimeJourneyStops{},
			}
		}

		realtimeJourney.ModificationDateTime = now

		for _, prediction := range predictions {
			stopID := fmt.Sprintf(ctdf.StopIDFormat, prediction.NaptanID)

			scheduledTime, _ := time.Parse(time.RFC3339, prediction.ExpectedArrival)
			scheduledTime = scheduledTime.In(now.Location())

			realtimeJourney.Stops[stopID] = &ctdf.RealtimeJourneyStops{
				StopRef:       stopID,
				TimeType:      ctdf.RealtimeJourneyStopTimeEstimatedFuture,
				ArrivalTime:   scheduledTime,
				DepartureTime: scheduledTime,
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
