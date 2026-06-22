package nrod

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"go.mongodb.org/mongo-driver/bson"
)

type TrustReinstatement struct {
	EventType  string `json:"event_type"`
	TrainID    string `json:"train_id"`
	OperatorID string `json:"toc_id"`
}

func (r *TrustReinstatement) Process(stompClient *StompClient) {
	now := time.Now()
	realtimeJourney, _ := realtimestore.FindByMapping(context.Background(), "TrainID", r.TrainID)

	if realtimeJourney == nil {
		log.Debug().Str("trainid", r.TrainID).Str("toc", r.OperatorID).Msg("Could not find Realtime Journey for train reinstatement")
		return
	}

	realtimeJourney.ModificationDateTime = now
	realtimeJourney.ActivelyTracked = true
	realtimeJourney.Cancelled = false

	realtimestore.SaveRealtimeJourney(context.Background(), realtimeJourney)

	// Also delete the service alert for it
	// TODO this doesnt seem to delete anything
	journeyRunDate := realtimeJourney.JourneyRunDate.Format("2006-01-02")
	serviceAlertID := fmt.Sprintf("GB:RAILCANCELDELAY:%s:%s", journeyRunDate, realtimeJourney.Journey.PrimaryIdentifier)

	serviceAlertCollection := database.GetCollection("service_alerts")

	filter := bson.M{"primaryidentifier": serviceAlertID}
	serviceAlertCollection.DeleteOne(context.Background(), filter)

	log.Info().
		Str("trainid", r.TrainID).
		Str("realtimejourney", realtimeJourney.PrimaryIdentifier).
		Msg("Train reinstated")
}
