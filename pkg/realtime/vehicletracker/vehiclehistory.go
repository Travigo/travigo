package vehicletracker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/travigo/travigo/pkg/redis_client"
)

const vehicleHistoryTTL = 15 * time.Minute

type vehicleJourneyHistory struct {
	JourneyID  string    `json:"journey_id"`
	Progress   float64   `json:"progress"`
	RecordedAt time.Time `json:"recorded_at"`
}

func vehicleHistoryKey(event *VehicleUpdateEvent) string {
	if event == nil || event.VehicleLocationUpdate == nil {
		return ""
	}
	vehicleID := event.VehicleLocationUpdate.VehicleIdentifier
	datasetID := event.VehicleLocationUpdate.IdentifyingInformation["LinkedDataset"]
	if vehicleID == "" || datasetID == "" {
		return ""
	}
	return fmt.Sprintf("realtime-vehicle-history:%s:%s", datasetID, vehicleID)
}

func loadVehicleJourneyHistory(ctx context.Context, event *VehicleUpdateEvent) *vehicleJourneyHistory {
	key := vehicleHistoryKey(event)
	if key == "" {
		return nil
	}
	value, err := redis_client.Client.Get(ctx, key).Bytes()
	if err != nil {
		return nil
	}
	var history vehicleJourneyHistory
	if json.Unmarshal(value, &history) != nil || history.JourneyID == "" {
		return nil
	}
	return &history
}

func storeVehicleJourneyHistory(ctx context.Context, event *VehicleUpdateEvent, journeyID string, progress float64) {
	key := vehicleHistoryKey(event)
	if key == "" || journeyID == "" {
		return
	}
	value, err := json.Marshal(vehicleJourneyHistory{JourneyID: journeyID, Progress: progress, RecordedAt: event.RecordedAt})
	if err == nil {
		_ = redis_client.Client.Set(ctx, key, value, vehicleHistoryTTL).Err()
	}
}

func scoreLocationCandidate(distance, progress float64, journeyID string, history *vehicleJourneyHistory) float64 {
	score := distance
	if history == nil || history.JourneyID != journeyID {
		return score
	}
	score -= 50
	if progress+0.05 < history.Progress {
		score += 200
	}
	return score
}
