package realtimestore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"
)

type RailDetailedAllocationMigrationStats struct {
	Scanned  int
	Migrated int
	Skipped  int
	Failed   int
}

type legacyJourneyDetailedRail struct {
	VehicleType     string
	VehicleTypeName string
	PowerType       string
	VehicleIDs      []string
	TrainLength     int
	Carriages       []legacyRailCarriage

	Seating          []ctdf.JourneyDetailedRailSeating
	SleeperAvailable bool
	Sleepers         []ctdf.JourneyDetailedRailSeating

	SpeedKMH        int
	AirConditioning bool
	WiFi            bool
	Toilets         bool
	PowerPlugs      bool
	USBPlugs        bool
	DisabledAccess  bool
	BicycleSpaces   bool

	ReservationRequired     bool
	ReservationBikeRequired bool
	ReservationRecommended  bool

	CateringAvailable   bool
	CateringDescription string
	ReplacementBus      bool
}

type legacyRailCarriage struct {
	ID           string
	CarriageType string
	Class        ctdf.JourneyDetailedRailSeating
	Toilets      []ctdf.RailCarriageToilet

	CarriageID             string
	VehicleID              string
	VehiclePosition        int
	FleetID                string
	SpecificType           string
	Livery                 string
	SpecialCharacteristics string
	VehicleStatus          string
	RegisteredStatus       string
	LengthMM               int
	WeightKG               int
	SeatCount              int
	Occupancy              int
}

type railDetailedAllocationMigrationResult int

const (
	railDetailedAllocationSkipped railDetailedAllocationMigrationResult = iota
	railDetailedAllocationMigrated
)

// MigrateLegacyRailDetailedAllocations is a temporary deployment migration.
// Remove its startup callers after all 24-hour allocation keys have been rewritten.
func MigrateLegacyRailDetailedAllocations(ctx context.Context) (RailDetailedAllocationMigrationStats, error) {
	stats := RailDetailedAllocationMigrationStats{}
	seenKeys := map[string]struct{}{}
	iter := redis_client.Client.Scan(ctx, 0, realtimeJourneyRailDetailedKey("allocation", "*"), 0).Iterator()

	for iter.Next(ctx) {
		key := iter.Val()
		if _, seen := seenKeys[key]; seen {
			continue
		}
		seenKeys[key] = struct{}{}
		stats.Scanned++

		result, err := migrateLegacyRailDetailedAllocationKey(ctx, key)
		if err != nil {
			stats.Failed++
			log.Error().Err(err).Str("key", key).Msg("Failed to migrate legacy rail allocation")
			continue
		}
		if result == railDetailedAllocationMigrated {
			stats.Migrated++
		} else {
			stats.Skipped++
		}
	}

	return stats, iter.Err()
}

func migrateLegacyRailDetailedAllocationKey(ctx context.Context, key string) (railDetailedAllocationMigrationResult, error) {
	for attempt := 0; attempt < 3; attempt++ {
		result := railDetailedAllocationSkipped
		err := redis_client.Client.Watch(ctx, func(tx *redis.Tx) error {
			current, err := tx.Get(ctx, key).Bytes()
			if err != nil {
				return err
			}

			migrated, legacy, err := convertLegacyRailDetailedAllocation(current)
			if err != nil {
				return err
			}
			if !legacy {
				return nil
			}

			migratedJSON, err := json.Marshal(migrated)
			if err != nil {
				return err
			}

			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, key, migratedJSON, redis.KeepTTL)
				return nil
			})
			if err == nil {
				result = railDetailedAllocationMigrated
			}
			return err
		}, key)
		if !errors.Is(err, redis.TxFailedErr) {
			return result, err
		}
	}

	return railDetailedAllocationSkipped, redis.TxFailedErr
}

func convertLegacyRailDetailedAllocation(data []byte) (ctdf.JourneyDetailedRail, bool, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return ctdf.JourneyDetailedRail{}, false, err
	}
	if _, newFormat := fields["Trains"]; newFormat {
		return ctdf.JourneyDetailedRail{}, false, nil
	}
	if _, legacyFormat := fields["Carriages"]; !legacyFormat {
		return ctdf.JourneyDetailedRail{}, false, nil
	}

	var legacy legacyJourneyDetailedRail
	if err := json.Unmarshal(data, &legacy); err != nil {
		return ctdf.JourneyDetailedRail{}, false, err
	}

	migrated := ctdf.JourneyDetailedRail{
		Seating:                 legacy.Seating,
		SleeperAvailable:        legacy.SleeperAvailable,
		Sleepers:                legacy.Sleepers,
		ReservationRequired:     legacy.ReservationRequired,
		ReservationBikeRequired: legacy.ReservationBikeRequired,
		ReservationRecommended:  legacy.ReservationRecommended,
		CateringAvailable:       legacy.CateringAvailable,
		CateringDescription:     legacy.CateringDescription,
		ReplacementBus:          legacy.ReplacementBus,
		Trains:                  legacyRailTrains(legacy),
	}

	return migrated, true, nil
}

func legacyRailTrains(legacy legacyJourneyDetailedRail) []ctdf.RailTrain {
	trains := []ctdf.RailTrain{}
	trainIndexes := map[string]int{}

	for _, carriage := range legacy.Carriages {
		trainID, carriageID, grouped := legacyRailCarriageIdentity(carriage, legacy.VehicleIDs)
		groupKey := trainID
		if !grouped {
			groupKey = "__legacy_single_train__"
		}

		trainIndex, found := trainIndexes[groupKey]
		if !found {
			trains = append(trains, ctdf.RailTrain{
				ID:       trainID,
				Position: len(trains) + 1,
				FleetID:  carriage.FleetID,
			})
			trainIndex = len(trains) - 1
			trainIndexes[groupKey] = trainIndex
		}

		train := &trains[trainIndex]
		if train.FleetID == "" {
			train.FleetID = carriage.FleetID
		}
		train.Carriages = append(train.Carriages, migrateLegacyRailCarriage(carriage, carriageID, train.FleetID))
	}

	for trainIndex := range trains {
		train := &trains[trainIndex]
		train.TrainLength = len(train.Carriages)
		applyLegacyRailTrainDetails(train, legacy)
	}

	return trains
}

func legacyRailCarriageIdentity(carriage legacyRailCarriage, vehicleIDs []string) (string, string, bool) {
	if carriage.VehicleID != "" && carriage.VehicleID != carriage.CarriageID {
		return carriage.VehicleID, carriage.ID, true
	}

	if trainID, carriageID, found := strings.Cut(carriage.ID, ":"); found {
		return trainID, carriageID, true
	}

	if len(vehicleIDs) == 1 {
		return vehicleIDs[0], carriage.ID, true
	}

	return "", carriage.ID, false
}

func migrateLegacyRailCarriage(carriage legacyRailCarriage, carriageID string, fleetID string) ctdf.RailCarriage {
	seatingClass := carriage.Class
	carriageType := carriage.CarriageType
	if parsedSeatingClass := legacySeatingClass(carriageType); parsedSeatingClass != "" {
		if seatingClass == "" {
			seatingClass = parsedSeatingClass
		}
		carriageType = ""
	}
	if carriageType == fleetID {
		carriageType = carriage.SpecificType
	}

	vehicleID := carriage.CarriageID
	if vehicleID == "" && carriage.VehicleID == "" {
		vehicleID = carriageID
	}

	return ctdf.RailCarriage{
		ID:                     carriageID,
		CarriageType:           carriageType,
		SeatingClass:           seatingClass,
		Toilets:                carriage.Toilets,
		CarriageID:             carriage.CarriageID,
		VehicleID:              vehicleID,
		VehiclePosition:        carriage.VehiclePosition,
		SpecificType:           carriage.SpecificType,
		Livery:                 carriage.Livery,
		SpecialCharacteristics: carriage.SpecialCharacteristics,
		VehicleStatus:          carriage.VehicleStatus,
		RegisteredStatus:       carriage.RegisteredStatus,
		LengthMM:               carriage.LengthMM,
		WeightKG:               carriage.WeightKG,
		SeatCount:              carriage.SeatCount,
		Occupancy:              carriage.Occupancy,
	}
}

func applyLegacyRailTrainDetails(train *ctdf.RailTrain, legacy legacyJourneyDetailedRail) {
	if train.FleetID == "" {
		train.VehicleType = legacy.VehicleType
		train.VehicleTypeName = legacy.VehicleTypeName
		train.PowerType = legacy.PowerType
	}

	train.SpeedKMH = legacy.SpeedKMH
	train.AirConditioning = legacy.AirConditioning
	train.WiFi = legacy.WiFi
	train.Toilets = legacy.Toilets
	train.PowerPlugs = legacy.PowerPlugs
	train.USBPlugs = legacy.USBPlugs
	train.DisabledAccess = legacy.DisabledAccess
	train.BicycleSpaces = legacy.BicycleSpaces
}

func legacySeatingClass(value string) ctdf.JourneyDetailedRailSeating {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "first", "f", "1":
		return ctdf.JourneyDetailedRailSeatingFirst
	case "standard", "s", "2":
		return ctdf.JourneyDetailedRailSeatingStandard
	default:
		return ""
	}
}
