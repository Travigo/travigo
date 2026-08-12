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
	d.ArrivalJourneyOffsets = nil
	d.ArrivalJourneys = nil
	d.corridorMu.Lock()
	d.corridors = nil
	d.corridorClock = 0
	d.corridorMu.Unlock()
	if !d.TopologyReady || len(d.Stops) == 0 || len(d.TransferOffsets) != len(d.Stops)+1 || len(d.StopIndexByStringID) == 0 {
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

	arrivalCounts := make([]uint32, len(d.Stops))
	for _, path := range d.Paths {
		if stop, exists := d.stopIndexForStringID(path.DestinationStopRef); exists {
			arrivalCounts[stop]++
		}
	}
	d.ArrivalJourneyOffsets = offsetsForCounts(arrivalCounts)
	d.ArrivalJourneys = make([]journeyID, d.ArrivalJourneyOffsets[len(d.Stops)])
	arrivalCursors := append([]uint32(nil), d.ArrivalJourneyOffsets[:len(d.Stops)]...)
	for journeyIndex, journey := range d.Journeys {
		for pathIndex := uint32(0); pathIndex < journey.PathCount; pathIndex++ {
			pathOffset := journey.PathStart + pathIndex
			if int(pathOffset) >= len(d.Paths) {
				break
			}
			if stop, exists := d.stopIndexForStringID(d.Paths[pathOffset].DestinationStopRef); exists {
				d.ArrivalJourneys[arrivalCursors[stop]] = journeyID(journeyIndex)
				arrivalCursors[stop]++
			}
		}
	}
	d.StaticRoutingReady = true
	log.Info().
		Int("reverse_transfer_links", len(d.ReverseTransferOrigins)).
		Int("static_ride_arrivals", len(d.ArrivalJourneys)).
		Dur("duration", time.Since(started)).
		Msg("Journey graph static routing indexes built")
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
	journeyRides := make([]uint8, len(d.Journeys))
	for index := range minimumRides {
		minimumRides[index] = unreachableCorridorRides
	}
	for index := range journeyRides {
		journeyRides[index] = unreachableCorridorRides
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
			for index := d.ArrivalJourneyOffsets[stop]; index < d.ArrivalJourneyOffsets[stop+1]; index++ {
				journeyID := d.ArrivalJourneys[index]
				if int(journeyID) >= len(d.Journeys) || journeyRides[journeyID] <= uint8(rides+1) {
					continue
				}
				journeyRides[journeyID] = uint8(rides + 1)
				journey := d.Journeys[journeyID]
				for pathIndex := uint32(0); pathIndex < journey.PathCount; pathIndex++ {
					pathOffset := journey.PathStart + pathIndex
					if int(pathOffset) >= len(d.Paths) {
						break
					}
					origin, exists := d.stopIndexForStringID(d.Paths[pathOffset].OriginStopRef)
					if exists && minimumRides[origin] > uint8(rides+1) {
						minimumRides[origin] = uint8(rides + 1)
						buckets[rides+1] = append(buckets[rides+1], origin)
					}
				}
			}
		}
	}
	return minimumRides
}
