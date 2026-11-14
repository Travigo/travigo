package ctdf

import (
	"fmt"
	"iter"
	"slices"
	"time"
)

// JourneyCall represents one visit to a stop. At a gap the previous segment
// ends and a new segment starts, so no leg connects those two calls.
type JourneyCall struct {
	StopRef   string    `bson:"stopref" groups:"basic,departureboard-cache,web-journey"`
	Stop      *Stop     `bson:"-" json:",omitempty" groups:"basic,web-journey"`
	Arrival   time.Time `bson:"arrival,omitempty" groups:"basic,departureboard-cache,web-journey"`
	Departure time.Time `bson:"departure,omitempty" groups:"basic,departureboard-cache,web-journey"`
	Platform  string    `bson:"platform,omitempty" json:",omitempty" groups:"basic,departureboard-cache,web-journey"`

	Activity []JourneyPathItemActivity `bson:"activity" groups:"basic,departureboard-cache,web-journey"`
	Start    bool                      `bson:"start,omitempty" json:",omitempty" groups:"basic,departureboard-cache,web-journey"`
	End      bool                      `bson:"end,omitempty" json:",omitempty" groups:"basic,departureboard-cache,web-journey"`
	Outgoing *JourneyLeg               `bson:"outgoing,omitempty" json:",omitempty" groups:"basic,departureboard-cache,web-journey"`
}

// JourneyLeg holds metadata for the connection to the next call. Its
// coordinates and provenance are populated from journey_tracks on demand.
type JourneyLeg struct {
	Distance           int                  `bson:"distance,omitempty" groups:"basic"`
	DestinationDisplay string               `bson:"destinationdisplay,omitempty" groups:"basic,departureboard-cache,web-journey"`
	TrackRef           string               `bson:"trackref,omitempty" json:",omitempty" groups:"internal"`
	PassingPath        []string             `bson:"passingpath,omitempty" json:"-"`
	Track              []Location           `bson:"-" json:",omitempty" groups:"basic,web-journey"`
	TrackDataSource    *DataSourceReference `bson:"-" json:",omitempty" groups:"basic,web-journey"`
}

// LegCalls yields the endpoints of each leg in order, skipping gaps.
func (j *Journey) LegCalls() iter.Seq2[*JourneyCall, *JourneyCall] {
	return func(yield func(*JourneyCall, *JourneyCall) bool) {
		if j == nil {
			return
		}
		for index := 0; index+1 < len(j.Calls); index++ {
			origin, destination := &j.Calls[index], &j.Calls[index+1]
			if !origin.End && !destination.Start && !yield(origin, destination) {
				return
			}
		}
	}
}

func (j *Journey) LegCount() int {
	count := 0
	for range j.LegCalls() {
		count++
	}
	return count
}

// StopCalls yields the realtime stop sequence. A segment end and the
// following segment start share an index in the existing occurrence space.
// Departure-oriented consumers use the new segment's origin at that index.
func (j *Journey) StopCalls() iter.Seq2[int, *JourneyCall] {
	return func(yield func(int, *JourneyCall) bool) {
		if j == nil {
			return
		}
		index := 0
		for offset := range j.Calls {
			call := &j.Calls[offset]
			if call.End && offset+1 < len(j.Calls) {
				continue
			}
			if !yield(index, call) {
				return
			}
			index++
		}
	}
}

// LegEndpoints returns the calls connected by a zero-based leg index.
func (j *Journey) LegEndpoints(index int) (origin, destination *JourneyCall) {
	if j == nil || index < 0 {
		return nil, nil
	}
	for from, to := range j.LegCalls() {
		if index == 0 {
			return from, to
		}
		index--
	}
	return nil, nil
}

// StopCall returns the call at the realtime stop index. A segment end and
// the following segment start share an index in the legacy occurrence space.
// Departure-oriented consumers use the new segment's origin at that index.
func (j *Journey) StopCall(index int) *JourneyCall {
	for occurrence, call := range j.StopCalls() {
		if index == occurrence {
			return call
		}
	}
	return nil
}

// RefreshStopRefs maintains the side-specific membership used by covered SIRI
// probes and board queries. Call it after replacing the call sequence.
func (j *Journey) RefreshStopRefs() {
	j.OriginStopRefs = nil
	j.DestinationStopRefs = nil
	origins := make(map[string]struct{}, len(j.Calls))
	destinations := make(map[string]struct{}, len(j.Calls))
	for origin, destination := range j.LegCalls() {
		if _, found := origins[origin.StopRef]; !found {
			j.OriginStopRefs = append(j.OriginStopRefs, origin.StopRef)
			origins[origin.StopRef] = struct{}{}
		}
		if _, found := destinations[destination.StopRef]; !found {
			j.DestinationStopRefs = append(j.DestinationStopRefs, destination.StopRef)
			destinations[destination.StopRef] = struct{}{}
		}
	}
}

func (j *Journey) ValidatePath() error {
	if j.Calls == nil {
		return fmt.Errorf("journey %q has no calls array", j.PrimaryIdentifier)
	}
	if len(j.Track) > 0 && j.TrackRef == "" {
		return fmt.Errorf("journey %q has a whole track without a reference", j.PrimaryIdentifier)
	}
	for index, call := range j.Calls {
		start := index == 0 || j.Calls[index-1].End
		last := index == len(j.Calls)-1
		if call.StopRef == "" || call.Start != start || (last && !call.End) || (call.Start && call.End) ||
			(call.End && call.Outgoing != nil) {
			return fmt.Errorf("journey %q has an invalid call at %d", j.PrimaryIdentifier, index)
		}
		if call.Outgoing != nil && len(call.Outgoing.Track) > 0 && call.Outgoing.TrackRef == "" {
			return fmt.Errorf("journey %q has a track without a reference at call %d", j.PrimaryIdentifier, index)
		}
	}
	return nil
}

// SetLegs accepts existing feed timing-link builders. The resulting journey
// only retains the call sequence.
func (j *Journey) SetLegs(legs []*JourneyPathItem) error {
	calls, err := journeyCallsFromLegs(legs)
	if err != nil {
		return fmt.Errorf("journey %q: %w", j.PrimaryIdentifier, err)
	}
	j.Calls = calls
	j.RefreshStopRefs()
	return nil
}

// JourneyCallsFromLegs constructs calls from prevalidated timing links.
func JourneyCallsFromLegs(legs ...*JourneyPathItem) []JourneyCall {
	calls, err := journeyCallsFromLegs(legs)
	if err != nil {
		panic(err)
	}
	return calls
}

func journeyCallsFromLegs(legs []*JourneyPathItem) ([]JourneyCall, error) {
	calls := make([]JourneyCall, 0, len(legs)+1)
	for index, leg := range legs {
		if leg == nil {
			return nil, fmt.Errorf("missing leg at %d", index)
		}
		call := JourneyCall{
			StopRef: leg.OriginStopRef, Stop: leg.OriginStop,
			Arrival: leg.OriginArrivalTime, Departure: leg.OriginDepartureTime,
			Platform: leg.OriginPlatform, Activity: leg.OriginActivity,
		}
		if leg.Distance != 0 || leg.DestinationDisplay != "" || leg.TrackRef != "" ||
			len(leg.PassingPath) != 0 || len(leg.Track) != 0 || leg.TrackDataSource != nil {
			call.Outgoing = &JourneyLeg{
				Distance: leg.Distance, DestinationDisplay: leg.DestinationDisplay,
				TrackRef: leg.TrackRef, PassingPath: leg.PassingPath,
				Track: leg.Track, TrackDataSource: leg.TrackDataSource,
			}
		}

		if index == 0 {
			call.Start = true
		} else {
			incoming := legs[index-1]
			if incoming.DestinationStopRef == leg.OriginStopRef {
				if call.Stop == nil {
					call.Stop = incoming.DestinationStop
				}
				if !incoming.DestinationArrivalTime.IsZero() {
					call.Arrival = incoming.DestinationArrivalTime
				}
				if call.Platform == "" {
					call.Platform = incoming.DestinationPlatform
				}
				call.Activity = mergeJourneyCallActivity(incoming.DestinationActivity, leg.OriginActivity)
			} else {
				calls = append(calls, journeySegmentEnd(incoming))
				call.Start = true
			}
		}
		calls = append(calls, call)
	}
	if len(legs) > 0 {
		calls = append(calls, journeySegmentEnd(legs[len(legs)-1]))
	}
	return calls, nil
}

// Timing-link feeds can disagree about activity at the same visit. The
// incoming link governs alighting and the outgoing link governs boarding.
func mergeJourneyCallActivity(incoming, outgoing []JourneyPathItemActivity) []JourneyPathItemActivity {
	if len(incoming) == 0 && len(outgoing) == 0 {
		return outgoing
	}
	pickup := journeyActivityAllows(outgoing, JourneyPathItemActivityPickup)
	setdown := journeyActivityAllows(incoming, JourneyPathItemActivitySetdown)
	switch {
	case pickup && setdown:
		return []JourneyPathItemActivity{JourneyPathItemActivityPickup, JourneyPathItemActivitySetdown}
	case pickup:
		return []JourneyPathItemActivity{JourneyPathItemActivityPickup}
	case setdown:
		return []JourneyPathItemActivity{JourneyPathItemActivitySetdown}
	default:
		return []JourneyPathItemActivity{JourneyPathItemActivityPass}
	}
}

func journeyActivityAllows(activity []JourneyPathItemActivity, permission JourneyPathItemActivity) bool {
	return len(activity) == 0 || slices.Contains(activity, permission)
}

func journeySegmentEnd(incoming *JourneyPathItem) JourneyCall {
	return JourneyCall{
		StopRef: incoming.DestinationStopRef, Stop: incoming.DestinationStop,
		Arrival: incoming.DestinationArrivalTime, Platform: incoming.DestinationPlatform,
		Activity: incoming.DestinationActivity, End: true,
	}
}

// Leg exposes the legacy view of one timing link. Mutate Calls directly.
func (j *Journey) Leg(index int) *JourneyPathItem {
	if origin, destination := j.LegEndpoints(index); origin != nil {
		leg := journeyPathItem(origin, destination)
		return &leg
	}
	return nil
}

// Legs builds the legacy public path from the current calls.
func (j *Journey) Legs() []*JourneyPathItem {
	if j.Calls == nil {
		return nil
	}
	items := make([]JourneyPathItem, j.LegCount())
	legs := make([]*JourneyPathItem, 0, len(items))
	for origin, destination := range j.LegCalls() {
		item := &items[len(legs)]
		*item = journeyPathItem(origin, destination)
		legs = append(legs, item)
	}
	return legs
}

func journeyPathItem(origin, destination *JourneyCall) JourneyPathItem {
	item := JourneyPathItem{
		OriginStopRef: origin.StopRef, OriginStop: origin.Stop,
		OriginArrivalTime: origin.Arrival, OriginDepartureTime: origin.Departure,
		OriginPlatform: origin.Platform, OriginActivity: origin.Activity,
		DestinationStopRef: destination.StopRef, DestinationStop: destination.Stop,
		DestinationArrivalTime: destination.Arrival,
		DestinationPlatform:    destination.Platform, DestinationActivity: destination.Activity,
	}
	if origin.Outgoing != nil {
		item.Distance = origin.Outgoing.Distance
		item.DestinationDisplay = origin.Outgoing.DestinationDisplay
		item.PassingPath = origin.Outgoing.PassingPath
		item.TrackRef = origin.Outgoing.TrackRef
		item.Track = origin.Outgoing.Track
		item.TrackDataSource = origin.Outgoing.TrackDataSource
	}
	return item
}
