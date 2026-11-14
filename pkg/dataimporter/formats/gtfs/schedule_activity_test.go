package gtfs

import (
	"reflect"
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestGTFSStopTimeActivity(t *testing.T) {
	tests := []struct {
		name     string
		pickup   int8
		dropOff  int8
		activity []ctdf.JourneyPathItemActivity
	}{
		{
			name:     "regular pickup and drop-off",
			activity: []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivitySetdown, ctdf.JourneyPathItemActivityPickup},
		},
		{
			name:     "conditional pickup and prohibited drop-off",
			pickup:   2,
			dropOff:  1,
			activity: []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivityPickup},
		},
		{
			name:     "prohibited pickup and conditional drop-off",
			pickup:   1,
			dropOff:  3,
			activity: []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivitySetdown},
		},
		{
			name:     "both prohibited",
			pickup:   1,
			dropOff:  1,
			activity: []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivityPass},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gtfsStopTimeActivity(StopTime{PickupType: tt.pickup, DropOffType: tt.dropOff})
			if !reflect.DeepEqual(got, tt.activity) {
				t.Fatalf("activity = %v, want %v", got, tt.activity)
			}
		})
	}
}
