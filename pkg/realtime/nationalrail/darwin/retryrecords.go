package darwin

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/nationalrail/railutils"
	"go.mongodb.org/mongo-driver/bson"
)

func RetryRecords(queue *railutils.BatchProcessingQueue) {
	retryRecordsCollection := database.GetCollection("retry_records")
	realtimeJourneysCollection := database.GetCollection("realtime_journeys")

	for true {
		matchingFormations := []ScheduleFormations{}

		var retryRecords []struct {
			Type   string
			Record ScheduleFormations
		}
		cursor, _ := retryRecordsCollection.Find(context.Background(), bson.M{"type": "realtimedarwin_formation"})
		cursor.All(context.Background(), &retryRecords)

		for _, retryRecord := range retryRecords {
			searchQuery := bson.M{"otheridentifiers.nationalrailrid": retryRecord.Record.RID}

			var realtimeJourney *ctdf.RealtimeJourney

			realtimeJourneysCollection.FindOne(context.Background(), searchQuery).Decode(&realtimeJourney)
			if realtimeJourney != nil {
				log.Info().
					Str("nationalrailrid", retryRecord.Record.RID).
					Str("realtimejourney", realtimeJourney.PrimaryIdentifier).
					Msg("Found matching record for retry schedule formation")

				matchingFormations = append(matchingFormations, retryRecord.Record)

				retryRecordsCollection.DeleteOne(context.Background(), searchQuery)
			}
		}

		pushPort := PushPortData{
			ScheduleFormations: matchingFormations,
		}
		pushPort.UpdateRealtimeJourneys(queue)

		// Run this loop every 90 seconds
		time.Sleep(90 * time.Second)
	}
}
