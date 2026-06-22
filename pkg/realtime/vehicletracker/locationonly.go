package vehicletracker

import (
	"context"
	"fmt"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
)

func (consumer *BatchConsumer) updateRealtimeJourneyLocationOnly(journeyID string, vehicleUpdateEvent *VehicleUpdateEvent) error {
	realtimeJourneyID := fmt.Sprintf(ctdf.RealtimeJourneyIDFormat, vehicleUpdateEvent.VehicleLocationUpdate.Timeframe, journeyID)

	if vehicleUpdateEvent.VehicleLocationUpdate.Location.Type != "" {
		return realtimestore.UpdateLocation(
			context.Background(),
			realtimeJourneyID,
			vehicleUpdateEvent.VehicleLocationUpdate.Location,
			vehicleUpdateEvent.VehicleLocationUpdate.Bearing,
		)
	}

	return nil
}
