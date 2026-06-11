package vehicletracker

import (
	"context"
	"fmt"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func (consumer *BatchConsumer) updateRealtimeJourneyLocationOnly(journeyID string, vehicleUpdateEvent *VehicleUpdateEvent) (mongo.WriteModel, error) {
	currentTime := vehicleUpdateEvent.RecordedAt
	realtimeJourneyIdentifier := fmt.Sprintf(ctdf.RealtimeJourneyIDFormat, vehicleUpdateEvent.VehicleLocationUpdate.Timeframe, journeyID)
	searchQuery := bson.M{"primaryidentifier": realtimeJourneyIdentifier}

	var realtimeJourney *ctdf.RealtimeJourney

	ctx := context.Background()
	realtimeJourney, _ = realtimestore.GetRealtimeJourney(ctx, realtimeJourneyIdentifier)

	opts := options.FindOne().SetProjection(bson.D{
		{Key: "primaryidentifier", Value: 1},
	})

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	if realtimeJourney == nil {
		realtimeJourneysCollection.FindOne(ctx, searchQuery, opts).Decode(&realtimeJourney)
	}

	if realtimeJourney == nil {
		return nil, nil
	} else {
		realtimeJourney.ModificationDateTime = currentTime
		realtimeJourney.VehicleLocation = vehicleUpdateEvent.VehicleLocationUpdate.Location
		realtimeJourney.VehicleBearing = vehicleUpdateEvent.VehicleLocationUpdate.Bearing
		realtimestore.SetRealtimeJourney(ctx, realtimeJourney)

		updateMap := bson.M{
			"modificationdatetime": currentTime,
			"vehiclelocation":      vehicleUpdateEvent.VehicleLocationUpdate.Location,
			"vehiclebearing":       vehicleUpdateEvent.VehicleLocationUpdate.Bearing,
		}

		bsonRep, _ := bson.Marshal(bson.M{"$set": updateMap})
		updateModel := mongo.NewUpdateOneModel()
		updateModel.SetFilter(searchQuery)
		updateModel.SetUpdate(bsonRep)

		return updateModel, nil
	}
}
