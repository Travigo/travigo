package nrod

import (
	"time"

	"github.com/rs/zerolog/log"
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
	if stompClient.td != nil {
		now := time.Now()
		at, valid := railEventTime(c.CancellationTimestamp, now)
		if !valid {
			at = now
		}
		stompClient.td.invalidateTrain(c.TrainID, at)
	}
	log.Debug().Str("trainid", c.TrainID).Str("reason", c.CancellationReasonCode).Msg("Train cancelled")
}
