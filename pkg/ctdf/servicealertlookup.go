package ctdf

import (
	"context"
	"time"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

// ActiveJourneyCancellationAlertIDs returns the journey identifiers matched by
// currently valid JourneyCancelled service alerts. The caller supplies the
// candidate IDs so board generation performs one bounded lookup per board.
func ActiveJourneyCancellationAlertIDs(ctx context.Context, journeyIDs []string, now time.Time) (map[string]struct{}, error) {
	cancelledJourneyIDs := make(map[string]struct{})
	if len(journeyIDs) == 0 {
		return cancelledJourneyIDs, nil
	}

	journeyIDSet := make(map[string]struct{}, len(journeyIDs))
	for _, journeyID := range journeyIDs {
		if journeyID != "" {
			journeyIDSet[journeyID] = struct{}{}
		}
	}
	if len(journeyIDSet) == 0 {
		return cancelledJourneyIDs, nil
	}

	collection := database.GetCollection("service_alerts")
	cursor, err := collection.Find(ctx, bson.M{
		"alerttype":          ServiceAlertTypeJourneyCancelled,
		"matchedidentifiers": bson.M{"$in": journeyIDs},
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
			if _, found := journeyIDSet[identifier]; found {
				cancelledJourneyIDs[identifier] = struct{}{}
			}
		}
	}

	return cancelledJourneyIDs, cursor.Err()
}
