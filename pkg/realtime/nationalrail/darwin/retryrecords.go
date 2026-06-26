package darwin

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"go.mongodb.org/mongo-driver/bson"
)

func RetryRecords() {
	retryRecordsCollection := database.GetCollection("retry_records")

	for true {
		matchingFormations := []ScheduleFormations{}

		var retryRecords []struct {
			Type   string
			Record ScheduleFormations
		}
		cursor, _ := retryRecordsCollection.Find(context.Background(), bson.M{"type": "realtimedarwin_formation"})
		cursor.All(context.Background(), &retryRecords)

		for _, retryRecord := range retryRecords {
			// TODO: Delete retry records using the inserted retry record shape, not otheridentifiers.nationalrailrid.
			searchQuery := bson.M{"otheridentifiers.nationalrailrid": retryRecord.Record.RID}

			realtimeJourney, err := realtimestore.FindByMapping(context.Background(), "nationalrailrid", retryRecord.Record.RID)
			if err == nil && realtimeJourney != nil {
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
		pushPort.UpdateRealtimeJourneys()

		// Run this loop every 90 seconds
		time.Sleep(90 * time.Second)
	}
}
