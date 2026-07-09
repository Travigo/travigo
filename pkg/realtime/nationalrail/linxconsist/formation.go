package linxconsist

import (
	"fmt"
	"sort"
	"strings"

	"github.com/travigo/travigo/pkg/ctdf"
)

type LinxTrainIdentifier struct {
	Core                   string
	Headcode               string
	TrainUID               string
	WorkingTimetableHour   string
	StartDate              string
	Company                string
	OperationalTrainNumber string
}

func ParseTrainIdentifier(message PassengerTrainConsistMessage) LinxTrainIdentifier {
	identifier := LinxTrainIdentifier{
		OperationalTrainNumber: message.OperationalTrainNumber.OperationalTrainNumber,
	}

	if len(message.TrainOperationalIdentification.TransportOperationalIdentifiers) == 0 {
		return identifier
	}

	transportIdentifier := message.TrainOperationalIdentification.TransportOperationalIdentifiers[0]
	identifier.Core = transportIdentifier.Core
	identifier.StartDate = transportIdentifier.StartDate
	identifier.Company = transportIdentifier.Company

	if len(transportIdentifier.Core) >= 12 {
		identifier.Headcode = transportIdentifier.Core[0:4]
		identifier.TrainUID = transportIdentifier.Core[4:10]
		identifier.WorkingTimetableHour = transportIdentifier.Core[10:12]
	}

	return identifier
}

func BuildRailTrains(message PassengerTrainConsistMessage) []ctdf.RailTrain {
	allocations := append([]Allocation(nil), message.Allocations...)
	sort.SliceStable(allocations, func(i, j int) bool {
		return allocations[i].ResourceGroupPosition < allocations[j].ResourceGroupPosition
	})

	trains := make([]ctdf.RailTrain, 0, len(allocations))
	for _, allocation := range allocations {
		vehicles := append([]Vehicle(nil), allocation.ResourceGroup.Vehicles...)
		sort.SliceStable(vehicles, func(i, j int) bool {
			return vehicles[i].ResourcePosition < vehicles[j].ResourcePosition
		})

		if strings.EqualFold(allocation.Reversed, "Y") {
			for left, right := 0, len(vehicles)-1; left < right; left, right = left+1, right-1 {
				vehicles[left], vehicles[right] = vehicles[right], vehicles[left]
			}
		}

		train := ctdf.RailTrain{
			ID:                  allocation.ResourceGroup.ResourceGroupID,
			Position:            allocation.ResourceGroupPosition,
			AllocationSequence:  allocation.AllocationSequenceNumber,
			ValidFrom:           allocation.AllocationOriginDateTime,
			ValidUntil:          allocation.AllocationDestinationDateTime,
			Reversed:            strings.EqualFold(allocation.Reversed, "Y"),
			FleetID:             allocation.ResourceGroup.FleetID,
			ResourceGroupType:   allocation.ResourceGroup.TypeOfResource,
			ResourceGroupStatus: allocation.ResourceGroup.ResourceGroupStatus,
			TrainLength:         len(vehicles),
			Carriages:           make([]ctdf.RailCarriage, 0, len(vehicles)),
		}

		for position, vehicle := range vehicles {
			train.Carriages = append(train.Carriages, ctdf.RailCarriage{
				ID:              fmt.Sprintf("%s:%s", allocation.ResourceGroup.ResourceGroupID, vehicle.VehicleID),
				CarriageType:    carriageType(vehicle),
				CarriageID:      vehicle.VehicleID,
				VehicleID:       vehicle.VehicleID,
				VehiclePosition: position + 1,
				// PlannedResourceGroup:   vehicle.PlannedResourceGroup,
				SpecificType: vehicle.SpecificType,
				Livery:       vehicle.Livery,
				// Decor:                  vehicle.Decor,
				SpecialCharacteristics: vehicle.SpecialCharacteristics,
				VehicleStatus:          vehicle.VehicleStatus,
				RegisteredStatus:       vehicle.RegisteredStatus,
				LengthMM:               lengthInMM(vehicle.Length),
				WeightKG:               vehicle.Weight,
				SeatCount:              vehicle.NumberOfSeats,
				// MaximumSpeedMPH:        vehicle.MaximumSpeed,
				Occupancy: -1,
			})
		}

		trains = append(trains, train)
	}

	return trains
}

func carriageType(vehicle Vehicle) string {
	if vehicle.SpecificType != "" {
		return vehicle.SpecificType
	}
	return vehicle.TypeOfVehicle
}

func lengthInMM(length Measure) int {
	switch strings.ToLower(length.Measure) {
	case "mm":
		return length.Value
	case "m":
		return length.Value * 1000
	case "ft":
		return int(float64(length.Value) * 304.8)
	default:
		return length.Value
	}
}
