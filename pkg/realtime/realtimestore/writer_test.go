package realtimestore

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"go.mongodb.org/mongo-driver/bson"
)

func TestLocationFields(t *testing.T) {
	modificationTime := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	location := ctdf.Location{
		Type:        "Point",
		Coordinates: []float64{-0.1, 51.5},
	}

	fields := LocationFields(location, 123.4, modificationTime)

	if got := fields["vehiclelocation"]; !reflect.DeepEqual(got, location) {
		t.Fatalf("vehiclelocation = %#v, want %#v", got, location)
	}
	if got := fields["vehiclebearing"]; got != 123.4 {
		t.Fatalf("vehiclebearing = %#v, want 123.4", got)
	}
	if got := fields["modificationdatetime"]; got != modificationTime {
		t.Fatalf("modificationdatetime = %#v, want %#v", got, modificationTime)
	}
}

func TestArrivalFields(t *testing.T) {
	modificationTime := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	arrivalTime := modificationTime.Add(5 * time.Minute)
	stopUpdate := &ctdf.RealtimeJourneyStops{
		ArrivalTime: arrivalTime,
		TimeType:    ctdf.RealtimeJourneyStopTimeEstimatedFuture,
	}

	fields := ArrivalFields(map[string]*ctdf.RealtimeJourneyStops{
		"gb-atco-123": stopUpdate,
		"":            &ctdf.RealtimeJourneyStops{StopRef: "gb-atco-456", ArrivalTime: arrivalTime},
		"ignored":     nil,
	}, modificationTime)

	if got := fields["modificationdatetime"]; got != modificationTime {
		t.Fatalf("modificationdatetime = %#v, want %#v", got, modificationTime)
	}
	if got := fields["stops.gb-atco-123"]; got != stopUpdate {
		t.Fatalf("stops.gb-atco-123 = %#v, want %#v", got, stopUpdate)
	}
	if got := fields["stops.gb-atco-456"]; got == nil {
		t.Fatal("stops.gb-atco-456 was not set")
	}
	if _, ok := fields["stops.ignored"]; ok {
		t.Fatal("nil stop update should not be included")
	}
}

func TestUpdateFieldsModelValidation(t *testing.T) {
	if _, err := UpdateFieldsModel("", bson.M{"field": "value"}); !errors.Is(err, ErrEmptyIdentifier) {
		t.Fatalf("empty identifier error = %v, want %v", err, ErrEmptyIdentifier)
	}

	if _, err := UpdateFieldsModel("realtime-test", bson.M{}); !errors.Is(err, ErrEmptyUpdate) {
		t.Fatalf("empty update error = %v, want %v", err, ErrEmptyUpdate)
	}

	if _, err := UpdateFieldsModel("realtime-test", bson.M{"field": "value"}, WithUpsert(true)); err != nil {
		t.Fatalf("UpdateFieldsModel returned error: %v", err)
	}
}

func TestReadFilters(t *testing.T) {
	identifierFilter := FilterByIdentifier("realtime-test")
	if got := identifierFilter["primaryidentifier"]; got != "realtime-test" {
		t.Fatalf("primaryidentifier = %#v, want realtime-test", got)
	}

	otherIdentifierFilter := FilterByOtherIdentifier("TrainID", "1234")
	if got := otherIdentifierFilter["otheridentifiers.TrainID"]; got != "1234" {
		t.Fatalf("otheridentifiers.TrainID = %#v, want 1234", got)
	}
}

func TestGetByIdentifierValidation(t *testing.T) {
	if _, err := GetByIdentifier(nil, ""); !errors.Is(err, ErrEmptyIdentifier) {
		t.Fatalf("empty identifier error = %v, want %v", err, ErrEmptyIdentifier)
	}
}
