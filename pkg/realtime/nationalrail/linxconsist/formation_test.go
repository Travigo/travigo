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

func TestBuildRailCarriagesPreservesFormationDetails(t *testing.T) {
	carriages := BuildRailCarriages(PassengerTrainConsistMessage{
		Allocations: []Allocation{
			{
				ResourceGroupPosition: 2,
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

	if len(carriages) != 3 {
		t.Fatalf("expected 3 carriages, got %d", len(carriages))
	}
	if carriages[0].ID != "156406:52406" {
		t.Fatalf("expected reversed leading carriage 156406:52406, got %s", carriages[0].ID)
	}
	if carriages[0].VehicleID != "52406" || carriages[0].ResourceGroupID != "156406" {
		t.Fatalf("expected vehicle and resource group ids to be preserved, got %+v", carriages[0])
	}
	if carriages[0].FleetID != "156" || carriages[0].SpecificType != "DMSL" || carriages[0].Livery != "AT" || carriages[0].Decor != "ZZ" {
		t.Fatalf("expected vehicle presentation fields to be preserved, got %+v", carriages[0])
	}
	if carriages[0].ResourceGroupType != "U" || carriages[0].ResourceGroupStatus != "N" || carriages[0].VehicleStatus != "N" || carriages[0].RegisteredStatus != "C" {
		t.Fatalf("expected resource and vehicle status fields to be preserved, got %+v", carriages[0])
	}
	if carriages[0].Occupancy != -1 {
		t.Fatalf("expected occupancy to be unknown, got %d", carriages[0].Occupancy)
	}
	if carriages[2].LengthMM != 20000 {
		t.Fatalf("expected metres to be converted to mm, got %d", carriages[2].LengthMM)
	}
}
