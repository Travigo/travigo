package nrod

import (
	"context"

	"github.com/kr/pretty"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type TrustCancellation struct {
	EventType  string `json:"event_type"`
	TrainID    string `json:"train_id"`
	OperatorID string `json:"toc_id"`

	TrainFileAddress        string `json:"train_file_address"`
	TrainServiceCode        string `json:"train_service_code"`
	DivisionCode            string `json:"division_code"`
	LocationStanox          string `json:"loc_stanox"`
	DepartureTimestamp      string `json:"dep_timestamp"`
	CancellationType        string `json:"canx_type"`
	CancellationTimestamp   string `json:"canx_timestamp"`
	OriginLocationStanox    string `json:"orig_loc_stanox"`
	OriginLocationTimestamp string `json:"orig_loc_timestamp"`
	CancellationReasonCode  string `json:"canx_reason_code"`
}

func (c *TrustCancellation) Process(stompClient *StompClient) {
	realtimeJourneysCollection := database.GetCollection("realtime_journeys")

	var realtimeJourney *ctdf.RealtimeJourney

	realtimeJourneysCollection.FindOne(context.Background(), bson.M{"otheridentifiers.TrainID": c.TrainID}).Decode(&realtimeJourney)
	if realtimeJourney == nil {
		log.Debug().Str("trainid", c.TrainID).Str("toc", c.OperatorID).Msg("Could not find Realtime Journey for train cancellation")
		return
	}

	log.Info().Msg("Train cancelled")
	pretty.Println(c, realtimeJourney)
}
