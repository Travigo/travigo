package vehicletracker

import (
	"context"
	"fmt"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func (consumer *BatchConsumer) updateRealtimeJourneyLocationOnly(journeyID string, vehicleUpdateEvent *VehicleUpdateEvent) (mongo.WriteModel, error) {
	currentTime := vehicleUpdateEvent.RecordedAt
	realtimeJourneyIdentifier := fmt.Sprintf(ctdf.RealtimeJourneyIDFormat, vehicleUpdateEvent.VehicleLocationUpdate.Timeframe, journeyID)
	searchQuery := bson.M{"primaryidentifier": realtimeJourneyIdentifier}

	var realtimeJourney *ctdf.RealtimeJourney

	opts := options.FindOne().SetProjection(bson.D{
		{Key: "primaryidentifier", Value: 1},
	})

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	realtimeJourneysCollection.FindOne(context.Background(), searchQuery, opts).Decode(&realtimeJourney)

	if realtimeJourney == nil {
		return nil, nil
	} else {
		updateMap := bson.M{
			"modificationdatetime": currentTime,
			"vehiclelocation":      vehicleUpdateEvent.VehicleLocationUpdate.Location,
			"vehiclebearing":       vehicleUpdateEvent.VehicleLocationUpdate.Bearing,
		}

		bsonRep, _ := bson.Marshal(bson.M{"$set": updateMap})
		updateModel := mongo.NewUpdateOneModel()
		updateModel.SetFilter(searchQuery)
		updateModel.SetUpdate(bsonRep)
		updateModel.SetUpsert(true)

		return updateModel, nil
	}
}
