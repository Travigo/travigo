package nrod

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type TrustReinstatement struct {
	EventType  string `json:"event_type"`
	TrainID    string `json:"train_id"`
	OperatorID string `json:"toc_id"`
}

func (r *TrustReinstatement) Process(stompClient *StompClient) {
	now := time.Now()

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")

	var realtimeJourney *ctdf.RealtimeJourney

	realtimeJourneysCollection.FindOne(context.Background(), bson.M{"otheridentifiers.TrainID": r.TrainID}).Decode(&realtimeJourney)
	if realtimeJourney == nil {
		log.Debug().Str("trainid", r.TrainID).Str("toc", r.OperatorID).Msg("Could not find Realtime Journey for train reinstatement")
		return
	}

	updateMap := bson.M{
		"modificationdatetime": now,
		"activelytracked":      true,
		"cancelled":            false,
	}

	// Create update
	bsonRep, _ := bson.Marshal(bson.M{"$set": updateMap})
	updateModel := mongo.NewUpdateOneModel()
	updateModel.SetFilter(bson.M{"primaryidentifier": realtimeJourney.PrimaryIdentifier})
	updateModel.SetUpdate(bsonRep)
	updateModel.SetUpsert(true)

	stompClient.Queue.Add(updateModel)

	log.Info().
		Str("trainid", r.TrainID).
		Str("realtimejourney", realtimeJourney.PrimaryIdentifier).
		Msg("Train reinstated")
}
