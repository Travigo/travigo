package telemetry

import (
	"sync/atomic"
	"time"
)

type SiriFallback int

const (
	SiriJourneyRef SiriFallback = iota
	SiriBlockRef
	SiriStops
	SiriStopsExactTime // Retained in reports for compatibility. Stops now use one query.
	siriFallbackCount
)

var siriFallbackNames = [siriFallbackCount]string{"journey_ref", "block_ref", "stops", "stops_exact_time"}

var siriFallbacks [siriFallbackCount]struct {
	lookup, narrowing              measurement
	candidates, available, matches atomic.Int64
}

type SiriFallbackReport struct {
	Lookup     Timing `json:"lookup"`
	Narrowing  Timing `json:"narrowing"`
	Candidates int64  `json:"candidates"`
	Available  int64  `json:"available"`
	Matches    int64  `json:"matches"`
}

func ObserveSiriFallback(fallback SiriFallback, lookup, narrowing time.Duration, candidates, available int, matched bool) {
	m := &siriFallbacks[fallback]
	observe(&m.lookup, lookup)
	observe(&m.narrowing, narrowing)
	m.candidates.Add(int64(candidates))
	m.available.Add(int64(available))
	if matched {
		m.matches.Add(1)
	}
}
