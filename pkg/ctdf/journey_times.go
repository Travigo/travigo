package ctdf

import (
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

// InterpolateMissingJourneyTimes resolves untimed intermediate calls evenly
// between their surrounding timed calls. Known arrival/departure times and dwell
// times are preserved, including service-day times beyond 24:00. Missing endpoint
// times and backwards timetables are rejected rather than invented.
func InterpolateMissingJourneyTimes(journey *Journey) error {
	if journey == nil || journey.Calls == nil {
		return fmt.Errorf("journey has no path")
	}
	if len(journey.Calls) == 0 {
		return nil
	}
	if journey.LegCount() == 0 {
		return fmt.Errorf("journey has no path")
	}
	for start := 0; start < len(journey.Calls); {
		end := start + 1
		for end < len(journey.Calls) && !journey.Calls[end-1].End {
			end++
		}
		if err := interpolateJourneySegment(journey.Calls[start:end]); err != nil {
			return err
		}
		start = end
	}
	if journey.DepartureTime.IsZero() {
		journey.DepartureTime = journey.Calls[0].Departure
	}
	return nil
}

func interpolateJourneySegment(segment []JourneyCall) error {
	type times struct{ arrival, departure time.Time }
	calls := make([]times, len(segment))
	for index, call := range segment {
		arrival := call.ArrivalTime()
		if arrival.IsZero() && !call.End {
			arrival = call.Arrival
		}
		calls[index] = times{arrival, call.Departure}
	}
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
			calls[index] = times{estimated, estimated}
		}
		previous = next
	}
	for index := range segment {
		call := &segment[index]
		if call.Arrival.IsZero() {
			call.Arrival = calls[index].arrival
		}
		if call.IncomingArrival != nil && call.ArrivalTime().IsZero() {
			// Repair a missing incoming time without replacing the outgoing time.
			arrival := primitive.NewDateTimeFromTime(calls[index].arrival)
			call.IncomingArrival = &arrival
		}
		if !call.End {
			call.Departure = calls[index].departure
		}
	}
	return nil
}
