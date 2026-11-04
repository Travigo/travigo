package vehicletracker

import (
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/formats/gtfs/nyct"
	"testing"
)

func TestSubwayDoesNotGuessJourneyFromLocation(t *testing.T) {
	consumer := &BatchConsumer{}
	event := &VehicleUpdateEvent{VehicleLocationUpdate: &VehicleLocationUpdate{Location: ctdf.Location{Type: "Point", Coordinates: []float64{-73.9, 40.7}}}}
	if got := consumer.identifyJourneyFromLocation(event, "GTFS-RT", map[string]string{"LinkedDataset": nyct.SubwayDataset}); got != "" {
		t.Fatalf("guessed subway journey %q", got)
	}
}
