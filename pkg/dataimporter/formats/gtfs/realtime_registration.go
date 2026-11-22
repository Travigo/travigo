package gtfs

import (
	"slices"
	"time"

	"github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"github.com/travigo/travigo/pkg/realtime/vehicledataprovider/dvlaves"
)

// BODS positions refresh every few seconds. Only recent positions can supply
// a plate to predictions published separately in the same snapshot.
const registrationPositionMaxAge = 2 * time.Minute

type realtimeRegistrationRun struct {
	tripID, date, startTime string
}

func registrationRun(trip *gtfs.TripDescriptor) realtimeRegistrationRun {
	return realtimeRegistrationRun{trip.GetTripId(), trip.GetStartDate(), trip.GetStartTime()}
}

func indexRegistrationPositions(entities []*gtfs.FeedEntity, now time.Time) map[realtimeRegistrationRun]*gtfs.VehicleDescriptor {
	if !slices.ContainsFunc(entities, func(entity *gtfs.FeedEntity) bool { return entity.GetTripUpdate() != nil }) {
		return nil
	}
	positions := make(map[realtimeRegistrationRun]*gtfs.VehicleDescriptor)
	for _, entity := range entities {
		position := entity.GetVehicle()
		vehicle := position.GetVehicle()
		run := registrationRun(position.GetTrip())
		if vehicle == nil || run.tripID == "" || (position.Timestamp != nil &&
			now.Sub(time.Unix(int64(position.GetTimestamp()), 0)) > registrationPositionMaxAge) {
			continue
		}

		previous, found := positions[run]
		if !found {
			positions[run] = vehicle
			continue
		}

		// An ambiguous trip run cannot supply a plate to a separate prediction.
		plate := bodsVehicleRegistration(nil, vehicle)
		previousPlate := bodsVehicleRegistration(nil, previous)
		switch {
		case previous == nil || differentVehicleIDs(previous, vehicle) || (previousPlate != "" && plate != "" && previousPlate != plate):
			positions[run] = nil
		case (previousPlate == "" && plate != "") || (previous.GetId() == "" && vehicle.GetId() != "" && plate == previousPlate):
			positions[run] = vehicle
		}
	}
	return positions
}

func bodsVehicleRegistration(prediction, position *gtfs.VehicleDescriptor) string {
	if plate := dvlaves.NormalizeRegistration(prediction.GetLicensePlate()); plate != "" {
		return plate
	}
	if differentVehicleIDs(prediction, position) {
		position = nil
	}
	if plate := dvlaves.NormalizeRegistration(position.GetLicensePlate()); plate != "" {
		return plate
	}
	if plate := dvlaves.RegistrationFromVehicleRef(prediction.GetId()); plate != "" {
		return plate
	}
	return dvlaves.RegistrationFromVehicleRef(position.GetId())
}

func differentVehicleIDs(a, b *gtfs.VehicleDescriptor) bool {
	return a.GetId() != "" && b.GetId() != "" && a.GetId() != b.GetId()
}
