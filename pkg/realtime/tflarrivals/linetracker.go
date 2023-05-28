package tflarrivals

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"io"
	"time"
)

type Linetracker struct {
	LineID string
}

func (l Linetracker) Start() {

}

func (l Linetracker) ParseArrivals(reader io.Reader) {
	now := time.Now()
	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	var realtimeJourneyUpdateOperations []mongo.WriteModel

	jsonBytes, _ := io.ReadAll(reader)

	var lineArrivals []ArrivalPrediction
	json.Unmarshal(jsonBytes, &lineArrivals)

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

				DataSource: &ctdf.DataSource{
					OriginalFormat: "tfl-json",
					Provider:       "GB-TfL",
					Dataset:        fmt.Sprintf("line/%s/arrivals", l.LineID),
					Identifier:     string(now.Unix()),
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
