package calculator

import "github.com/travigo/travigo/pkg/realtime/realtimestore"

type RealtimeJourneyStats = realtimestore.RealtimeJourneyStats

func GetRealtimeJourneys() RealtimeJourneyStats {
	return realtimestore.GetRealtimeJourneys()
}
