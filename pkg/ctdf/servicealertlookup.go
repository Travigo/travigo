package ctdf

import (
	"context"
	"fmt"
	"time"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

// ActiveJourneyCancellationAlertIDs returns the journey identifiers matched by
// currently valid JourneyCancelled service alerts. The caller supplies the
// candidate IDs so board generation performs one bounded lookup per board.
func ActiveJourneyCancellationAlertIDs(ctx context.Context, journeyIDs []string, serviceDate time.Time, now time.Time) (map[string]struct{}, error) {
	cancelledJourneyIDs := make(map[string]struct{})
	if len(journeyIDs) == 0 {
		return cancelledJourneyIDs, nil
	}

	journeyIDByAlertIdentifier := make(map[string]string, len(journeyIDs)*2)
	for _, journeyID := range journeyIDs {
		if journeyID != "" {
			journeyIDByAlertIdentifier[journeyID] = journeyID
			journeyIDByAlertIdentifier[fmt.Sprintf("DAYINSTANCEOF:%s:%s", serviceDate.Format(YearMonthDayFormat), journeyID)] = journeyID
		}
	}
	if len(journeyIDByAlertIdentifier) == 0 {
		return cancelledJourneyIDs, nil
	}

	collection := database.GetCollection("service_alerts")
	cursor, err := collection.Find(ctx, bson.M{
		"alerttype":          ServiceAlertTypeJourneyCancelled,
		"matchedidentifiers": bson.M{"$in": mapKeys(journeyIDByAlertIdentifier)},
		"validfrom":          bson.M{"$lt": now},
		"validuntil":         bson.M{"$gt": now},
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	for cursor.Next(ctx) {
		var alert ServiceAlert
		if err := cursor.Decode(&alert); err != nil {
			return nil, err
		}
		for _, identifier := range alert.MatchedIdentifiers {
			if journeyID, found := journeyIDByAlertIdentifier[identifier]; found {
				cancelledJourneyIDs[journeyID] = struct{}{}
			}
		}
	}

	return cancelledJourneyIDs, cursor.Err()
}

func mapKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}
