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
			// Performance Review [Set L] [PR104] [P1] (realtime): Inserts store record.rid, so this
			// delete does not consume successful retries; they replay every 90s until
			// the two-hour TTL, repeating lookups/writes and possibly restoring an old
			// formation. Retain _id, deduplicate by run/source version, and page due
			// retries with bounded backoff. Delete that exact record only after the
			// allocation write succeeds; deleting before UpdateRealtimeJourneys would
			// lose it on failure once the selector is corrected. Fence stale versions.
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
