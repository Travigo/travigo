package departuregraph

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestMongoJourneyRepairResolvesStoredZeroTimes(t *testing.T) {
	journey := testJourney()
	journey.Path[0].DestinationArrivalTime = time.Time{}
	journey.Path[1].OriginArrivalTime = time.Time{}
	journey.Path[1].OriginDepartureTime = time.Time{}
	if err := repairMissingJourneyTimes(journey); err != nil {
		t.Fatal(err)
	}
	if journey.Path[1].OriginDepartureTime.IsZero() || !journey.Path[0].DestinationArrivalTime.Equal(journey.Path[1].OriginDepartureTime) {
		t.Fatal("stored intermediate zero times were not repaired")
	}
	data := newGraphData()
	data.addJourney(20261102, journey)
	if len(data.materializeStop(20261102, []string{"stop-b"})) != 1 {
		t.Fatal("repaired call was not indexed")
	}
	if err := repairMissingJourneyTimes(&ctdf.Journey{Path: []*ctdf.JourneyPathItem{{}}}); err == nil {
		t.Fatal("unresolvable stored journey was accepted")
	}
}

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

func TestJourneyServiceDateFilterScopesPrimaryAvailabilityRules(t *testing.T) {
	filter := journeyServiceDateFilter([]time.Time{
		time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.August, 11, 0, 0, 0, 0, time.UTC),
	})
	clauses, ok := filter["$or"].(bson.A)
	if !ok || len(clauses) != 4 {
		t.Fatalf("availability clauses = %#v", filter["$or"])
	}
	rendered := fmt.Sprint(filter)
	for _, expected := range []string{"MatchAll", "DateRange", "2026-08-10", "2026-08-11", "Monday", "Tuesday"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("filter %q does not contain %q", rendered, expected)
		}
	}
}

func TestJourneyServiceDateFilterRejectsEmptyWindow(t *testing.T) {
	filter := journeyServiceDateFilter(nil)
	if _, exists := filter["_id"]; !exists {
		t.Fatalf("empty window filter = %#v", filter)
	}
}
