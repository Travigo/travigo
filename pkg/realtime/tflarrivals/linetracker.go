package tflarrivals

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/eko/gocache/lib/v4/cache"
	"github.com/travigo/travigo/pkg/ctdf"
	"io"
	"time"
)

type Linetracker struct {
	LineID       string
	JourneyStore *cache.Cache[string]
}

func (l Linetracker) Start() {

}

func (l Linetracker) ParseArrivals(reader io.Reader) {
	now := time.Now()

	jsonBytes, _ := io.ReadAll(reader)

	var lineArrivals []ArrivalPrediction
	json.Unmarshal(jsonBytes, &lineArrivals)

	// Group all the arrivals predictions that are part of the same journey
	groupedLineArrivals := map[string][]ArrivalPrediction{}
	for _, arrival := range lineArrivals {
		realtimeJourneyID := fmt.Sprintf(
			"REALTIME:TFL:%s:%s:%s:%s:%s",
			arrival.ModeName,
			arrival.LineID,
			arrival.Direction,
			arrival.VehicleID,
			arrival.DestinationNaptanID,
		)

		groupedLineArrivals[realtimeJourneyID] = append(groupedLineArrivals[realtimeJourneyID], arrival)
	}

	// Generate RealtimeJourneys for each group
	for realtimeJourneyID, predictions := range groupedLineArrivals {
		previousJourneyJSON, err := l.JourneyStore.Get(context.Background(), realtimeJourneyID)

		var realtimeJourney ctdf.RealtimeJourney
		if err == nil {
			json.Unmarshal([]byte(previousJourneyJSON), &realtimeJourney)
		} else {
			realtimeJourney = ctdf.RealtimeJourney{
				PrimaryIdentifier: realtimeJourneyID,

				DataSource: &ctdf.DataSource{
					OriginalFormat: "tfl-json",
					Provider:       "GB-TfL",
					Dataset:        "line/arrivals",
					Identifier:     l.LineID,
				},

				Stops: map[string]*ctdf.RealtimeJourneyStops{},
			}
		}

		for _, prediction := range predictions {
			stopID := fmt.Sprintf(ctdf.StopIDFormat, prediction.NaptanID)

			scheduledTime, _ := time.Parse(time.RFC3339, prediction.ExpectedArrival)
			scheduledTime = scheduledTime.In(now.Location())

			realtimeJourney.Stops[stopID] = &ctdf.RealtimeJourneyStops{
				StopRef:       stopID,
				TimeType:      ctdf.RealtimeJourneyStopTimeEstimatedFuture,
				ArrivalTime:   scheduledTime,
				DepartureTime: scheduledTime,
			}
		}

		realtimeJourneyJSON, _ := json.Marshal(realtimeJourney)
		l.JourneyStore.Set(context.Background(), realtimeJourneyID, string(realtimeJourneyJSON))
	}
}
