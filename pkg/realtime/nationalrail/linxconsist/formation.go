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

func BuildRailCarriages(message PassengerTrainConsistMessage) []ctdf.RailCarriage {
	allocations := append([]Allocation(nil), message.Allocations...)
	sort.SliceStable(allocations, func(i, j int) bool {
		return allocations[i].ResourceGroupPosition < allocations[j].ResourceGroupPosition
	})

	carriages := []ctdf.RailCarriage{}
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

		for position, vehicle := range vehicles {
			carriages = append(carriages, ctdf.RailCarriage{
				ID:                     fmt.Sprintf("%s:%s", allocation.ResourceGroup.ResourceGroupID, vehicle.VehicleID),
				Class:                  carriageClass(allocation.ResourceGroup, vehicle),
				VehicleID:              vehicle.VehicleID,
				ResourceGroupID:        allocation.ResourceGroup.ResourceGroupID,
				ResourceGroupType:      allocation.ResourceGroup.TypeOfResource,
				ResourceGroupStatus:    allocation.ResourceGroup.ResourceGroupStatus,
				ResourceGroupPosition:  allocation.ResourceGroupPosition,
				VehiclePosition:        position + 1,
				PlannedResourceGroup:   vehicle.PlannedResourceGroup,
				FleetID:                allocation.ResourceGroup.FleetID,
				SpecificType:           vehicle.SpecificType,
				Livery:                 vehicle.Livery,
				Decor:                  vehicle.Decor,
				SpecialCharacteristics: vehicle.SpecialCharacteristics,
				VehicleStatus:          vehicle.VehicleStatus,
				RegisteredStatus:       vehicle.RegisteredStatus,
				LengthMM:               lengthInMM(vehicle.Length),
				WeightKG:               vehicle.Weight,
				SeatCount:              vehicle.NumberOfSeats,
				MaximumSpeedMPH:        vehicle.MaximumSpeed,
				Occupancy:              -1,
			})
		}
	}

	return carriages
}

func carriageClass(resourceGroup ResourceGroup, vehicle Vehicle) string {
	if resourceGroup.FleetID != "" {
		return resourceGroup.FleetID
	}
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
