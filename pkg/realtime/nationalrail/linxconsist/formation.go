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
			Carriages:           make([]ctdf.RailCarriage, 0, len(vehicles)),
		}

		for position, vehicle := range vehicles {
			role := vehicleRole(allocation.ResourceGroup.FleetID, vehicle)
			carriage := ctdf.RailCarriage{
				ID:              fmt.Sprintf("%s:%s", allocation.ResourceGroup.ResourceGroupID, vehicle.VehicleID),
				CarriageType:    carriageType(vehicle),
				VehicleRole:     role,
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
			}
			if carriage.CountsTowardsTrainLength() {
				train.TrainLength++
			}
			train.Carriages = append(train.Carriages, carriage)
		}

		trains = append(trains, train)
	}

	return trains
}

func vehicleRole(fleetID string, vehicle Vehicle) ctdf.RailCarriageVehicleRole {
	typeOfVehicle := normaliseVehicleType(vehicle.TypeOfVehicle)
	specificType := normaliseVehicleType(vehicle.SpecificType)

	if isPowerCarVehicle(fleetID, vehicle, typeOfVehicle, specificType) {
		return ctdf.RailCarriageVehicleRolePowerCar
	}

	if vehicle.NumberOfSeats > 0 {
		return ctdf.RailCarriageVehicleRolePassenger
	}

	switch typeOfVehicle {
	case "c", "coach", "carriage":
		return ctdf.RailCarriageVehicleRolePassenger
	}

	return ctdf.RailCarriageVehicleRoleUnknown
}

func isPowerCarVehicle(fleetID string, vehicle Vehicle, typeOfVehicle string, specificType string) bool {
	if strings.Contains(typeOfVehicle, "power") || strings.Contains(specificType, "power") {
		return true
	}

	if vehicle.NumberOfSeats == 0 {
		switch typeOfVehicle {
		case "l", "loco", "locomotive":
			return true
		}
		switch specificType {
		case "pc", "pp", "pwr", "powercar", "power-car", "powerpack", "power-pack", "power module", "power-module":
			return true
		}
	}

	fleetClass := leadingDigits(fleetID)
	if (fleetClass == "755" || fleetClass == "756") && vehicle.NumberOfSeats == 0 {
		lengthMM := lengthInMM(vehicle.Length)
		if lengthMM > 0 && lengthMM < 10000 {
			return true
		}
	}

	return false
}

func normaliseVehicleType(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func leadingDigits(value string) string {
	digits := ""
	for _, char := range value {
		if char >= '0' && char <= '9' {
			digits += string(char)
			continue
		}
		if digits != "" {
			break
		}
	}
	return digits
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
