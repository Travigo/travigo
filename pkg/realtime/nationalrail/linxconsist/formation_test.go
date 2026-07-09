package linxconsist

import "testing"

func TestParseTrainIdentifierFromCore(t *testing.T) {
	identifier := ParseTrainIdentifier(PassengerTrainConsistMessage{
		TrainOperationalIdentification: TrainOperationalIdentification{
			TransportOperationalIdentifiers: []TransportOperationalIdentifier{
				{
					Core:      "2P90Y5426721",
					StartDate: "2019-04-30",
					Company:   "9923",
				},
			},
		},
	})

	if identifier.Headcode != "2P90" {
		t.Fatalf("expected headcode 2P90, got %s", identifier.Headcode)
	}
	if identifier.TrainUID != "Y54267" {
		t.Fatalf("expected train uid Y54267, got %s", identifier.TrainUID)
	}
	if identifier.WorkingTimetableHour != "21" {
		t.Fatalf("expected WTT hour 21, got %s", identifier.WorkingTimetableHour)
	}
}

func TestBuildRailTrainsPreservesUnitBoundariesAndFormationDetails(t *testing.T) {
	trains := BuildRailTrains(PassengerTrainConsistMessage{
		Allocations: []Allocation{
			{
				AllocationSequenceNumber:      2,
				AllocationOriginDateTime:      "2026-07-09T12:00:00",
				AllocationDestinationDateTime: "2026-07-09T13:00:00",
				ResourceGroupPosition:         2,
				ResourceGroup: ResourceGroup{
					ResourceGroupID: "150234",
					FleetID:         "150/2",
					Vehicles: []Vehicle{
						{VehicleID: "57234", ResourcePosition: 1, SpecificType: "DMS", Livery: "NP", Length: Measure{Value: 20, Measure: "m"}},
					},
				},
			},
			{
				ResourceGroupPosition: 1,
				Reversed:              "Y",
				ResourceGroup: ResourceGroup{
					ResourceGroupID:     "156406",
					TypeOfResource:      "U",
					FleetID:             "156",
					ResourceGroupStatus: "N",
					Vehicles: []Vehicle{
						{VehicleID: "57406", ResourcePosition: 1, SpecificType: "DMS", Livery: "AT", Decor: "ZZ", NumberOfSeats: 70, MaximumSpeed: 75},
						{
							VehicleID:              "52406",
							ResourcePosition:       2,
							PlannedResourceGroup:   "156406",
							SpecificType:           "DMSL",
							Livery:                 "AT",
							Decor:                  "ZZ",
							SpecialCharacteristics: "GW",
							NumberOfSeats:          68,
							VehicleStatus:          "N",
							RegisteredStatus:       "C",
							MaximumSpeed:           75,
						},
					},
				},
			},
		},
	})

	if len(trains) != 2 {
		t.Fatalf("expected 2 trains, got %d", len(trains))
	}
	if trains[0].ID != "156406" || trains[0].FleetID != "156" || trains[0].Position != 1 {
		t.Fatalf("expected first resource group to remain a separate train, got %+v", trains[0])
	}
	if trains[1].ID != "150234" || trains[1].TrainLength != 1 {
		t.Fatalf("expected second resource group to remain a separate train, got %+v", trains[1])
	}
	if trains[1].AllocationSequence != 2 || trains[1].ValidFrom != "2026-07-09T12:00:00" || trains[1].ValidUntil != "2026-07-09T13:00:00" {
		t.Fatalf("expected allocation journey portion to be preserved, got %+v", trains[1])
	}
	if !trains[0].Reversed {
		t.Fatalf("expected reversed allocation state to be preserved")
	}

	firstCarriage := trains[0].Carriages[0]
	if firstCarriage.ID != "156406:52406" {
		t.Fatalf("expected reversed leading carriage 156406:52406, got %s", firstCarriage.ID)
	}
	if firstCarriage.CarriageID != "52406" || firstCarriage.VehicleID != "52406" {
		t.Fatalf("expected carriage vehicle id to be preserved, got %+v", firstCarriage)
	}
	if firstCarriage.CarriageType != "DMSL" || firstCarriage.SpecificType != "DMSL" || firstCarriage.Livery != "AT" {
		t.Fatalf("expected vehicle presentation fields to be preserved, got %+v", firstCarriage)
	}
	if firstCarriage.SeatingClass != "" {
		t.Fatalf("expected LINX vehicle type not to be treated as seating class, got %+v", firstCarriage)
	}
	if firstCarriage.Occupancy != -1 {
		t.Fatalf("expected occupancy to be unknown, got %d", firstCarriage.Occupancy)
	}
	if trains[1].Carriages[0].LengthMM != 20000 {
		t.Fatalf("expected metres to be converted to mm, got %d", trains[1].Carriages[0].LengthMM)
	}
}
