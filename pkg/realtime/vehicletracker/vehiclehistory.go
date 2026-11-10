package vehicletracker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/redis_client"
)

const vehicleHistoryTTL = 15 * time.Minute

var saveVehicleHistory = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if ARGV[4] then
	if current ~= ARGV[4] then
		return false
	end
elseif current then
	local ok, history = pcall(cjson.decode, current)
	local timestamp = ok and type(history) == 'table' and history.recorded_at
	if type(timestamp) == 'string' then
		if not string.match(timestamp, '^%d%d%d%d%-%d%d%-%d%dT%d%d:%d%d:%d%d[%.%d]*Z$') then
			return current
		end
		-- Go's UTC timestamps sort by time once the trailing Z is removed.
		if string.sub(timestamp, 1, -2) >= string.sub(ARGV[2], 1, -2) then
			return false
		end
	end
end
redis.call('SET', KEYS[1], ARGV[1], 'EX', ARGV[3])
return false
`)

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

func storeVehicleJourneyHistory(ctx context.Context, key string, recordedAt time.Time, journeyID string, progress float64) {
	if journeyID == "" {
		return
	}
	recordedAt = recordedAt.UTC()
	value, err := json.Marshal(vehicleJourneyHistory{JourneyID: journeyID, Progress: progress, RecordedAt: recordedAt})
	if err != nil {
		return
	}
	args := []interface{}{value, recordedAt.Format(time.RFC3339Nano), int64(vehicleHistoryTTL / time.Second)}
	legacy, err := saveVehicleHistory.Run(ctx, redis_client.Client, []string{key}, args...).Text()
	if err != nil {
		return
	}

	// Older writers could store an offset instead of UTC. Parse it in Go and
	// conditionally replace the same value so a concurrent write still wins.
	var previous vehicleJourneyHistory
	if json.Unmarshal([]byte(legacy), &previous) == nil && !recordedAt.After(previous.RecordedAt) {
		return
	}
	_ = saveVehicleHistory.Run(ctx, redis_client.Client, []string{key}, append(args, legacy)...).Err()
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
