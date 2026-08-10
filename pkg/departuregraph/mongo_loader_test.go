package departuregraph

import (
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestMongoJourneyRecordDecodesIdentifierAndProjectedJourney(t *testing.T) {
	identifier := primitive.NewObjectID()
	encoded, err := bson.Marshal(bson.M{
		"_id":               identifier,
		"primaryidentifier": "journey-1",
		"serviceref":        "service-1",
	})
	if err != nil {
		t.Fatalf("marshal projected journey: %v", err)
	}

	var record mongoJourneyRecord
	if err := bson.Unmarshal(encoded, &record); err != nil {
		t.Fatalf("decode projected journey: %v", err)
	}
	if record.ID != identifier || record.Journey.PrimaryIdentifier != "journey-1" || record.Journey.ServiceRef != "service-1" {
		t.Fatalf("unexpected projected journey: %#v", record)
	}
}
