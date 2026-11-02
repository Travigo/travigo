package ctdf

import (
	"fmt"
	"time"
)

// InterpolateMissingJourneyTimes resolves untimed intermediate calls evenly
// between their surrounding timed calls. Known arrival/departure times and dwell
// times are preserved, including service-day times beyond 24:00. Missing endpoint
// times and backwards timetables are rejected rather than invented.
func InterpolateMissingJourneyTimes(journey *Journey) error {
	if journey == nil || len(journey.Path) == 0 {
		return fmt.Errorf("journey has no path")
	}
	type call struct{ arrival, departure time.Time }
	calls := make([]call, len(journey.Path)+1)
	for index, path := range journey.Path {
		if path == nil {
			return fmt.Errorf("nil journey path at %d", index)
		}
		arrival := path.OriginArrivalTime
		if index > 0 && !journey.Path[index-1].DestinationArrivalTime.IsZero() {
			arrival = journey.Path[index-1].DestinationArrivalTime
		}
		calls[index] = call{arrival, path.OriginDepartureTime}
	}
	calls[len(calls)-1].arrival = journey.Path[len(journey.Path)-1].DestinationArrivalTime
	for index := range calls {
		if calls[index].arrival.IsZero() {
			calls[index].arrival = calls[index].departure
		}
		if calls[index].departure.IsZero() {
			calls[index].departure = calls[index].arrival
		}
		if calls[index].departure.Before(calls[index].arrival) {
			return fmt.Errorf("departure precedes arrival at call %d", index)
		}
	}
	if calls[0].departure.IsZero() || calls[len(calls)-1].arrival.IsZero() {
		return fmt.Errorf("missing journey endpoint time")
	}
	previous := 0
	for next := 1; next < len(calls); next++ {
		if calls[next].arrival.IsZero() {
			continue
		}
		start, end := calls[previous].departure, calls[next].arrival
		if end.Before(start) {
			return fmt.Errorf("arrival precedes previous departure at call %d", next)
		}
		for index := previous + 1; index < next; index++ {
			estimated := start.Add((end.Sub(start) / time.Duration(next-previous)) * time.Duration(index-previous)).Truncate(time.Second)
			calls[index] = call{estimated, estimated}
		}
		previous = next
	}
	for index, path := range journey.Path {
		path.OriginArrivalTime = calls[index].arrival
		path.OriginDepartureTime = calls[index].departure
		path.DestinationArrivalTime = calls[index+1].arrival
	}
	if journey.DepartureTime.IsZero() {
		journey.DepartureTime = calls[0].departure
	}
	return nil
}
