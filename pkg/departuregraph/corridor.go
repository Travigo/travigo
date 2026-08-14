package departuregraph

import (
	"math"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	unreachableCorridorRides = uint8(math.MaxUint8)
	maximumCachedCorridors   = 8
)

type corridorKey struct {
	Destination    uint32
	MaxVehicleLegs uint8
}

type corridorCacheEntry struct {
	minimumRides []uint8
	lastUsed     uint64
}

func (d *graphData) buildStaticRoutingIndexesLocked() {
	started := time.Now()
	d.StaticRoutingReady = false
	d.ReverseTransferOffsets = nil
	d.ReverseTransferOrigins = nil
	d.ArrivalPatternOffsets = nil
	d.ArrivalPatterns = nil
	d.StaticPatterns = nil
	d.StaticPatternStops = nil
	d.corridorMu.Lock()
	d.corridors = nil
	d.corridorClock = 0
	d.corridorMu.Unlock()
	if !d.TopologyReady || len(d.CompleteDays) == 0 || len(d.Stops) == 0 || len(d.TransferOffsets) != len(d.Stops)+1 || len(d.StopIndexByStringID) == 0 {
		return
	}

	transferCounts := make([]uint32, len(d.Stops))
	for origin := range d.Stops {
		for index := d.TransferOffsets[origin]; index < d.TransferOffsets[origin+1]; index++ {
			to := d.Transfers[index].ToStop
			if int(to) < len(d.Stops) {
				transferCounts[to]++
			}
		}
	}
	d.ReverseTransferOffsets = offsetsForCounts(transferCounts)
	d.ReverseTransferOrigins = make([]uint32, d.ReverseTransferOffsets[len(d.Stops)])
	transferCursors := append([]uint32(nil), d.ReverseTransferOffsets[:len(d.Stops)]...)
	for origin := range d.Stops {
		for index := d.TransferOffsets[origin]; index < d.TransferOffsets[origin+1]; index++ {
			to := d.Transfers[index].ToStop
			if int(to) >= len(d.Stops) {
				continue
			}
			d.ReverseTransferOrigins[transferCursors[to]] = uint32(origin)
			transferCursors[to]++
		}
	}

	d.buildStaticPatternsLocked()
	d.StaticRoutingReady = true
	log.Info().
		Int("reverse_transfer_links", len(d.ReverseTransferOrigins)).
		Int("static_patterns", len(d.StaticPatterns)).
		Int("static_ride_arrivals", len(d.ArrivalPatterns)).
		Dur("duration", time.Since(started)).
		Msg("Journey graph static routing indexes built")
}

func (d *graphData) buildStaticPatternsLocked() {
	byHash := make(map[uint64][]uint32)
	calls := make([]uint32, 0, 64)
	for _, journey := range d.Journeys {
		calls = calls[:0]
		if uint32(cap(calls)) < journey.PathCount+1 {
			calls = make([]uint32, 0, journey.PathCount+1)
		}
		for pathIndex := uint32(0); pathIndex < journey.PathCount; pathIndex++ {
			pathOffset := journey.PathStart + pathIndex
			if int(pathOffset) >= len(d.Paths) {
				break
			}
			path := d.Paths[pathOffset]
			if origin, exists := d.stopIndexForStringID(path.OriginStopRef); exists && (len(calls) == 0 || calls[len(calls)-1] != origin) {
				calls = append(calls, origin)
			}
			if destination, exists := d.stopIndexForStringID(path.DestinationStopRef); exists && (len(calls) == 0 || calls[len(calls)-1] != destination) {
				calls = append(calls, destination)
			}
		}
		if len(calls) < 2 {
			continue
		}
		hash := uint64(1469598103934665603)
		for _, stop := range calls {
			hash ^= uint64(stop) + 1
			hash *= 1099511628211
		}
		matched := false
		for _, patternID := range byHash[hash] {
			pattern := d.StaticPatterns[patternID]
			stored := d.StaticPatternStops[pattern.StopStart : pattern.StopStart+pattern.StopCount]
			if equalStopPattern(stored, calls) {
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		patternID := uint32(len(d.StaticPatterns))
		d.StaticPatterns = append(d.StaticPatterns, staticPatternRecord{StopStart: uint32(len(d.StaticPatternStops)), StopCount: uint32(len(calls))})
		d.StaticPatternStops = append(d.StaticPatternStops, calls...)
		byHash[hash] = append(byHash[hash], patternID)
	}

	arrivalCounts := make([]uint32, len(d.Stops))
	for _, pattern := range d.StaticPatterns {
		for position := uint32(1); position < pattern.StopCount; position++ {
			arrivalCounts[d.StaticPatternStops[pattern.StopStart+position]]++
		}
	}
	d.ArrivalPatternOffsets = offsetsForCounts(arrivalCounts)
	d.ArrivalPatterns = make([]uint64, d.ArrivalPatternOffsets[len(d.Stops)])
	arrivalCursors := append([]uint32(nil), d.ArrivalPatternOffsets[:len(d.Stops)]...)
	for patternID, pattern := range d.StaticPatterns {
		for position := uint32(1); position < pattern.StopCount; position++ {
			stop := d.StaticPatternStops[pattern.StopStart+position]
			d.ArrivalPatterns[arrivalCursors[stop]] = uint64(uint32(patternID))<<32 | uint64(position)
			arrivalCursors[stop]++
		}
	}
}

func equalStopPattern(left, right []uint32) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func offsetsForCounts(counts []uint32) []uint32 {
	offsets := make([]uint32, len(counts)+1)
	for index, count := range counts {
		offsets[index+1] = offsets[index] + count
	}
	return offsets
}

func (d *graphData) planCorridor(destinations map[uint32]bool, maxVehicleLegs int) []uint8 {
	if !d.StaticRoutingReady || len(destinations) == 0 || maxVehicleLegs < 0 || maxVehicleLegs >= int(unreachableCorridorRides) {
		return nil
	}
	var key corridorKey
	cacheable := len(destinations) == 1
	if cacheable {
		for destination := range destinations {
			key = corridorKey{Destination: destination, MaxVehicleLegs: uint8(maxVehicleLegs)}
		}
		d.corridorMu.Lock()
		if entry := d.corridors[key]; entry != nil {
			d.corridorClock++
			entry.lastUsed = d.corridorClock
			result := entry.minimumRides
			d.corridorMu.Unlock()
			return result
		}
		d.corridorMu.Unlock()
	}

	started := time.Now()
	minimumRides := d.buildPlanCorridor(destinations, maxVehicleLegs)
	reachableStops := 0
	for _, rides := range minimumRides {
		if rides != unreachableCorridorRides {
			reachableStops++
		}
	}
	log.Info().
		Int("destinations", len(destinations)).
		Int("max_vehicle_legs", maxVehicleLegs).
		Int("reachable_stops", reachableStops).
		Dur("duration", time.Since(started)).
		Msg("Journey graph static corridor built")
	if !cacheable {
		return minimumRides
	}
	d.corridorMu.Lock()
	defer d.corridorMu.Unlock()
	if d.corridors == nil {
		d.corridors = make(map[corridorKey]*corridorCacheEntry, maximumCachedCorridors)
	}
	d.corridorClock++
	d.corridors[key] = &corridorCacheEntry{minimumRides: minimumRides, lastUsed: d.corridorClock}
	if len(d.corridors) > maximumCachedCorridors {
		var oldestKey corridorKey
		oldestUse := uint64(math.MaxUint64)
		for candidateKey, entry := range d.corridors {
			if entry.lastUsed < oldestUse {
				oldestKey = candidateKey
				oldestUse = entry.lastUsed
			}
		}
		delete(d.corridors, oldestKey)
	}
	return minimumRides
}

func (d *graphData) buildPlanCorridor(destinations map[uint32]bool, maxVehicleLegs int) []uint8 {
	minimumRides := make([]uint8, len(d.Stops))
	patternRides := make([]uint8, len(d.StaticPatterns))
	for index := range minimumRides {
		minimumRides[index] = unreachableCorridorRides
	}
	for index := range patternRides {
		patternRides[index] = unreachableCorridorRides
	}
	buckets := make([][]uint32, maxVehicleLegs+1)
	for destination := range destinations {
		if int(destination) < len(minimumRides) && minimumRides[destination] != 0 {
			minimumRides[destination] = 0
			buckets[0] = append(buckets[0], destination)
		}
	}

	for rides := 0; rides <= maxVehicleLegs; rides++ {
		for cursor := 0; cursor < len(buckets[rides]); cursor++ {
			stop := buckets[rides][cursor]
			if minimumRides[stop] != uint8(rides) {
				continue
			}
			for index := d.ReverseTransferOffsets[stop]; index < d.ReverseTransferOffsets[stop+1]; index++ {
				origin := d.ReverseTransferOrigins[index]
				if minimumRides[origin] > uint8(rides) {
					minimumRides[origin] = uint8(rides)
					buckets[rides] = append(buckets[rides], origin)
				}
			}
			if rides == maxVehicleLegs {
				continue
			}
			for index := d.ArrivalPatternOffsets[stop]; index < d.ArrivalPatternOffsets[stop+1]; index++ {
				arrival := d.ArrivalPatterns[index]
				patternID, position := uint32(arrival>>32), uint32(arrival)
				if int(patternID) >= len(d.StaticPatterns) || patternRides[patternID] <= uint8(rides+1) {
					continue
				}
				patternRides[patternID] = uint8(rides + 1)
				pattern := d.StaticPatterns[patternID]
				if position > pattern.StopCount {
					continue
				}
				for callPosition := uint32(0); callPosition < position; callPosition++ {
					origin := d.StaticPatternStops[pattern.StopStart+callPosition]
					if minimumRides[origin] > uint8(rides+1) {
						minimumRides[origin] = uint8(rides + 1)
						buckets[rides+1] = append(buckets[rides+1], origin)
					}
				}
			}
		}
	}
	return minimumRides
}
