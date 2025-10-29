package vehicletracker

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func (consumer *BatchConsumer) updateRealtimeJourneyLocationOnly(journeyID string, vehicleUpdateEvent *VehicleUpdateEvent) (mongo.WriteModel, error) {
	currentTime := vehicleUpdateEvent.RecordedAt
	ctx := context.Background()

	realtimeJourneyIdentifier := fmt.Sprintf(ctdf.RealtimeJourneyIDFormat, vehicleUpdateEvent.VehicleLocationUpdate.Timeframe, journeyID)
	searchQuery := bson.M{"primaryidentifier": realtimeJourneyIdentifier}

	// Try to get cached journey state first
	cachedState, _ := GetCachedJourneyState(ctx, realtimeJourneyIdentifier)

	// If we have cached state, use it for change detection without DB read
	if cachedState != nil {
		newLocation := vehicleUpdateEvent.VehicleLocationUpdate.Location
		newBearing := vehicleUpdateEvent.VehicleLocationUpdate.Bearing

		// For location-only updates, we use the cached values for things we're not updating
		shouldWrite, reason := cachedState.ShouldWriteToDatabase(
			newLocation,
			newBearing,
			cachedState.LastOccupancy,  // Use cached value
			cachedState.NextStopRef,    // Use cached value
			cachedState.DepartedStopRef, // Use cached value
			cachedState.Offset,         // Use cached value
			currentTime,
			consumer.changeDetectionConfig,
		)

		// Update cache with new location/bearing
		newCachedState := &CachedRealtimeJourney{
			PrimaryIdentifier: cachedState.PrimaryIdentifier,
			NextStopRef:       cachedState.NextStopRef,
			DepartedStopRef:   cachedState.DepartedStopRef,
			Offset:            cachedState.Offset,
			LastLocation:      newLocation,
			LastBearing:       newBearing,
			LastOccupancy:     cachedState.LastOccupancy,
			LastUpdate:        currentTime,
			IsNew:             false,
		}

		if shouldWrite {
			newCachedState.LastDBWrite = currentTime
			log.Debug().
				Str("journey", realtimeJourneyIdentifier).
				Str("reason", reason).
				Msg("Writing location-only update to database")
		} else {
			newCachedState.LastDBWrite = cachedState.LastDBWrite
			SetCachedJourneyState(ctx, realtimeJourneyIdentifier, newCachedState)
			log.Debug().
				Str("journey", realtimeJourneyIdentifier).
				Str("reason", reason).
				Msg("Skipping location-only database write")
			return nil, nil
		}

		// Update cache before database write
		SetCachedJourneyState(ctx, realtimeJourneyIdentifier, newCachedState)

		updateMap := bson.M{
			"modificationdatetime": currentTime,
			"vehiclelocation":      newLocation,
			"vehiclebearing":       newBearing,
		}

		bsonRep, _ := bson.Marshal(bson.M{"$set": updateMap})
		updateModel := mongo.NewUpdateOneModel()
		updateModel.SetFilter(searchQuery)
		updateModel.SetUpdate(bsonRep)
		updateModel.SetUpsert(true)

		return updateModel, nil
	}

	// Fallback: no cache, check if journey exists in DB
	var realtimeJourney *ctdf.RealtimeJourney

	opts := options.FindOne().SetProjection(bson.D{
		{Key: "primaryidentifier", Value: 1},
	})

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	realtimeJourneysCollection.FindOne(ctx, searchQuery, opts).Decode(&realtimeJourney)

	if realtimeJourney == nil {
		return nil, nil
	}

	// Initialize cache for this journey
	newLocation := vehicleUpdateEvent.VehicleLocationUpdate.Location
	newBearing := vehicleUpdateEvent.VehicleLocationUpdate.Bearing

	newCachedState := &CachedRealtimeJourney{
		PrimaryIdentifier: realtimeJourneyIdentifier,
		LastLocation:      newLocation,
		LastBearing:       newBearing,
		LastUpdate:        currentTime,
		LastDBWrite:       currentTime,
		IsNew:             false,
	}
	SetCachedJourneyState(ctx, realtimeJourneyIdentifier, newCachedState)

	updateMap := bson.M{
		"modificationdatetime": currentTime,
		"vehiclelocation":      newLocation,
		"vehiclebearing":       newBearing,
	}

	bsonRep, _ := bson.Marshal(bson.M{"$set": updateMap})
	updateModel := mongo.NewUpdateOneModel()
	updateModel.SetFilter(searchQuery)
	updateModel.SetUpdate(bsonRep)
	updateModel.SetUpsert(true)

	return updateModel, nil
}
