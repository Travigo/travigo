package departuregraph

import (
	"bufio"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
)

type snapshotFile struct {
	Version                int
	WrittenAt              time.Time
	Strings                []string
	StopIdentifiers        []stopIdentifierRecord
	Stops                  []stopRecord
	StopAliasOffsets       []uint32
	StopAliases            []stringID
	StopGrid               map[spatialCell][]uint32
	TransferOffsets        []uint32
	Transfers              []transferRecord
	TransferRestrictions   []transferRestriction
	TopologyReady          bool
	ReverseTransferOffsets []uint32
	ReverseTransferOrigins []uint32
	ArrivalJourneyOffsets  []uint32
	ArrivalJourneys        []journeyID
	ArrivalPatternOffsets  []uint32
	ArrivalPatterns        []uint64
	StaticPatterns         []staticPatternRecord
	StaticPatternStops     []uint32
	JourneyPatterns        []uint32
	StaticRoutingReady     bool
	Journeys               []journeyRecord
	Paths                  []pathRecord
	Replacements           []stringID
	JourneyDays            map[journeyDayKey]bool
	DayJourneys            map[dayKey][]journeyID
	Departures             map[bucketKey][]journeyID // v1-v7
	Boardings              map[bucketKey][]departureEntry
	CompleteStops          map[bucketKey]bool
	CompleteDays           map[dayKey]bool
	ScanDays               []dayKey
	ScanCursor             string
	ScanProcessed          int64
	ScanActive             int64
}

// Save writes the current graph generation to the configured snapshot path.
// Reads can continue while the checkpoint is encoded.
func (g *Graph) Save() error {
	if g == nil || g.config.SnapshotPath == "" {
		return nil
	}
	return g.saveTracked(g.config.SnapshotPath, g.current.Load())
}

func (g *Graph) saveTracked(path string, data *graphData) error {
	g.snapshotMu.Lock()
	defer g.snapshotMu.Unlock()
	complete := data != nil && data.snapshotComplete()
	revision := g.snapshotRevision.Load()
	if complete && g.completeSnapshot.Load() && revision == g.snapshotSaved.Load() {
		log.Debug().Str("path", path).Msg("Departure graph checkpoint skipped because graph is unchanged")
		return nil
	}
	if !complete && g.completeSnapshot.Load() {
		log.Info().Str("path", path).Msg("Departure graph incomplete checkpoint skipped to preserve complete snapshot")
		return nil
	}
	started := g.metrics.snapshot.beginWrite()
	err := g.save(path, data)
	var size int64
	if info, statErr := os.Stat(path); statErr == nil {
		size = info.Size()
	}
	g.metrics.snapshot.finishWrite(started, size, err)
	if err == nil && complete {
		g.completeSnapshot.Store(true)
		g.snapshotSaved.Store(revision)
	}
	return err
}

func (g *Graph) restoreTracked(path string) error {
	info, statErr := os.Stat(path)
	if os.IsNotExist(statErr) {
		return nil
	}
	started := time.Now()
	if statErr != nil {
		g.metrics.snapshot.restored(started, 0, statErr)
		return statErr
	}
	err := g.restore(path)
	g.metrics.snapshot.restored(started, info.Size(), err)
	return err
}

func (g *Graph) save(path string, data *graphData) error {
	if path == "" || data == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	temporary, err := os.CreateTemp(filepath.Dir(path), ".departure-graph-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()

	checkpointWriter := newSnapshotCheckpointWriter(temporary)
	buffered := bufio.NewWriterSize(checkpointWriter, 1024*1024)
	compressor, err := zstd.NewWriter(buffered, zstd.WithEncoderLevel(zstd.SpeedFastest))
	if err != nil {
		_ = temporary.Close()
		return err
	}

	err = data.writeSnapshot(compressor)
	if err == nil {
		err = compressor.Close()
	} else {
		compressor.Close()
	}
	if err == nil {
		err = buffered.Flush()
	}
	if err == nil {
		err = checkpointWriter.Finalize()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}

// writeSnapshot captures array prefixes and bucket slice headers under the graph
// lock. Mutable index maps are copied; path and departure arrays are shared.
// The maintenance barrier keeps those prefixes immutable until encoding finishes.
// Ordinary append-only fills and scans keep running, and disk I/O holds no mu.
func (data *graphData) writeSnapshot(w io.Writer) error {
	data.checkpointMu.RLock()
	defer data.checkpointMu.RUnlock()
	data.mu.RLock()
	snapshot := snapshotFile{
		Version:                snapshotVersion,
		WrittenAt:              time.Now(),
		Strings:                data.Strings,
		StopIdentifiers:        data.StopIdentifiers,
		Stops:                  data.Stops,
		StopAliasOffsets:       data.StopAliasOffsets,
		StopAliases:            data.StopAliases,
		StopGrid:               data.StopGrid,
		TransferOffsets:        data.TransferOffsets,
		Transfers:              data.Transfers,
		TransferRestrictions:   data.TransferRestrictions,
		TopologyReady:          data.TopologyReady,
		ReverseTransferOffsets: data.ReverseTransferOffsets,
		ReverseTransferOrigins: data.ReverseTransferOrigins,
		ArrivalPatternOffsets:  data.ArrivalPatternOffsets,
		ArrivalPatterns:        data.ArrivalPatterns,
		StaticPatterns:         data.StaticPatterns,
		StaticPatternStops:     data.StaticPatternStops,
		JourneyPatterns:        data.JourneyPatterns,
		StaticRoutingReady:     data.StaticRoutingReady,
		Journeys:               data.Journeys,
		Paths:                  data.Paths,
		Replacements:           data.Replacements,
		JourneyDays:            maps.Clone(data.JourneyDays),
		DayJourneys:            maps.Clone(data.DayJourneys),
		Boardings:              maps.Clone(data.Departures),
		CompleteStops:          maps.Clone(data.CompleteStops),
		CompleteDays:           maps.Clone(data.CompleteDays),
		ScanDays:               append([]dayKey(nil), data.ScanDays...),
		ScanCursor:             data.ScanCursor,
		ScanProcessed:          data.ScanProcessed,
		ScanActive:             data.ScanActive,
	}
	data.mu.RUnlock()
	return encodeSnapshot(w, snapshot)
}

func (g *Graph) restore(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer file.Close()

	decoder, err := zstd.NewReader(bufio.NewReaderSize(file, 1024*1024))
	if err != nil {
		return err
	}
	defer decoder.Close()

	var snapshot snapshotFile
	if err := decodeSnapshot(decoder, &snapshot); err != nil {
		return err
	}
	if snapshot.Version < 1 || snapshot.Version > snapshotVersion {
		return fmt.Errorf("unsupported departure graph snapshot version %d", snapshot.Version)
	}
	if snapshot.Version < 4 {
		// Older snapshots stored every origin arrival on the path record. The
		// compact v4 record cannot decode that removed field, so use the first
		// departure as the origin-arrival fallback until the next rolling build.
		// Board and planner searches do not consume this first arrival directly.
		for index := range snapshot.Journeys {
			record := &snapshot.Journeys[index]
			if record.PathCount > 0 && int(record.PathStart) < len(snapshot.Paths) {
				record.InitialArrival = snapshot.Paths[record.PathStart].OriginDeparture
			}
		}
	}
	legacyDepartures := snapshot.Boardings == nil

	restored := &graphData{
		Strings:                snapshot.Strings,
		StopIDs:                map[string]stringID{"": 0},
		StopIdentifiers:        snapshot.StopIdentifiers,
		Stops:                  snapshot.Stops,
		StopAliasOffsets:       snapshot.StopAliasOffsets,
		StopAliases:            snapshot.StopAliases,
		StopGrid:               snapshot.StopGrid,
		TransferOffsets:        snapshot.TransferOffsets,
		Transfers:              snapshot.Transfers,
		TransferRestrictions:   snapshot.TransferRestrictions,
		TopologyReady:          snapshot.TopologyReady,
		ReverseTransferOffsets: snapshot.ReverseTransferOffsets,
		ReverseTransferOrigins: snapshot.ReverseTransferOrigins,
		ArrivalPatternOffsets:  snapshot.ArrivalPatternOffsets,
		ArrivalPatterns:        snapshot.ArrivalPatterns,
		StaticPatterns:         snapshot.StaticPatterns,
		StaticPatternStops:     snapshot.StaticPatternStops,
		JourneyPatterns:        snapshot.JourneyPatterns,
		StaticRoutingReady:     snapshot.StaticRoutingReady,
		Journeys:               snapshot.Journeys,
		Paths:                  snapshot.Paths,
		Replacements:           snapshot.Replacements,
		JourneyDays:            snapshot.JourneyDays,
		DayJourneys:            snapshot.DayJourneys,
		Departures:             snapshot.Boardings,
		CompleteStops:          snapshot.CompleteStops,
		CompleteDays:           snapshot.CompleteDays,
		ScanDays:               snapshot.ScanDays,
		ScanCursor:             snapshot.ScanCursor,
		ScanProcessed:          snapshot.ScanProcessed,
		ScanActive:             snapshot.ScanActive,
	}
	if !restored.validTopologyLocked() {
		if restored.TopologyReady {
			log.Warn().Msg("Snapshot topology is empty or inconsistent; reloading stops and transfers")
		}
		restored.TopologyReady = false
		restored.StaticRoutingReady = false
	}
	if len(restored.Strings) == 0 {
		restored.Strings = []string{""}
	}
	if restored.Departures == nil {
		restored.Departures = map[bucketKey][]departureEntry{}
	}
	if restored.JourneyDays == nil {
		restored.JourneyDays = map[journeyDayKey]bool{}
	}
	if restored.DayJourneys == nil {
		restored.DayJourneys = map[dayKey][]journeyID{}
		for day := range restored.CompleteDays {
			seen := make([]bool, len(restored.Journeys))
			if legacyDepartures {
				for key, journeys := range snapshot.Departures {
					if key.Day != day {
						continue
					}
					for _, journey := range journeys {
						if int(journey) < len(seen) {
							seen[journey] = true
						}
					}
				}
			} else {
				for key, journeys := range restored.Departures {
					if key.Day != day {
						continue
					}
					for _, entry := range journeys {
						journey := entry.journey()
						if int(journey) < len(seen) {
							seen[journey] = true
						}
					}
				}
			}
			for journey, active := range seen {
				if active {
					restored.DayJourneys[day] = append(restored.DayJourneys[day], journeyID(journey))
				}
			}
		}
	}
	if legacyDepartures {
		for day, journeys := range restored.DayJourneys {
			for _, journey := range journeys {
				if int(journey) >= len(restored.Journeys) {
					continue
				}
				record := restored.Journeys[journey]
				for pathIndex := uint32(0); pathIndex < record.PathCount; pathIndex++ {
					path := restored.Paths[record.PathStart+pathIndex]
					if path.OriginStopRef == 0 || path.OriginActivity == activitySetdown {
						continue
					}
					if entry, ok := packDepartureEntry(journey, pathIndex, path.OriginDeparture); ok {
						key := bucketKey{Day: day, StopRef: path.OriginStopRef}
						restored.Departures[key] = append(restored.Departures[key], entry)
					}
				}
			}
		}
		// Release the legacy four-byte index before rebuilding the static
		// corridor structures; migration temporarily holds both formats.
		snapshot.Departures = nil
		restored.sortDepartureBucketsLocked()
	}
	snapshot.ArrivalJourneyOffsets = nil
	snapshot.ArrivalJourneys = nil
	if restored.CompleteStops == nil {
		restored.CompleteStops = map[bucketKey]bool{}
	}
	if restored.CompleteDays == nil {
		restored.CompleteDays = map[dayKey]bool{}
	}
	if snapshot.Version < 9 {
		if err := restored.repairLegacySnapshotTimes(); err != nil {
			return err
		}
	}
	for _, path := range restored.Paths {
		for _, stopID := range []stringID{path.OriginStopRef, path.DestinationStopRef} {
			if value := restored.stringValue(stopID); value != "" {
				restored.StopIDs[value] = stopID
			}
		}
	}
	for _, identifier := range restored.StopIdentifiers {
		if value := restored.stringValue(identifier.Identifier); value != "" {
			restored.StopIDs[value] = identifier.Identifier
		}
	}
	restored.buildStopIndexByStringIDLocked()
	restored.rebuildIncomingJourneyStateStopsLocked()
	if len(restored.CompleteDays) > 0 {
		if !restored.StaticRoutingReady || len(restored.ReverseTransferOffsets) != len(restored.Stops)+1 || len(restored.ArrivalPatternOffsets) != len(restored.Stops)+1 || len(restored.JourneyPatterns) != len(restored.Journeys) {
			restored.buildStaticRoutingIndexesLocked()
		} else if restored.PatternDepartureBuckets == nil {
			restored.buildPatternDepartureIndexesLocked()
		}
	}
	for key := range restored.CompleteStops {
		if value := restored.stringValue(key.StopRef); value != "" {
			restored.StopIDs[value] = key.StopRef
		}
	}
	if len(restored.ScanDays) > 0 || len(restored.CompleteDays) == 0 {
		restored.ensureBuildIndexesLocked()
	} else {
		restored.sealBuildIndexesLocked()
	}

	g.current.Store(restored)
	if snapshot.Version < 9 {
		// Convert a complete legacy checkpoint even when no timetable changes.
		g.snapshotRevision.Add(1)
	}
	if restored.snapshotComplete() {
		g.completeSnapshot.Store(true)
	}

	stats := restored.stats()
	log.Info().
		Str("path", path).
		Time("written_at", snapshot.WrittenAt).
		Int("journeys", stats.Journeys).
		Int("paths", stats.Paths).
		Int("departure_buckets", stats.DepartureBuckets).
		Int("arrival_buckets", stats.ArrivalBuckets).
		Msg("Departure graph snapshot restored")
	return nil
}

// Legacy snapshots can contain the 366-day offset produced from time.Time{}.
// Repair only affected journeys, then replace their boarding entries. An
// unrepairable checkpoint is rejected so startup can rebuild from MongoDB.
func (d *graphData) repairLegacySnapshotTimes() error {
	repaired := map[journeyID]bool{}
	for index, record := range d.Journeys {
		_, initialOK := packDepartureEntry(0, 0, record.InitialArrival)
		_, departureOK := packDepartureEntry(0, 0, record.DepartureSeconds)
		invalid := !initialOK || !departureOK
		if uint64(record.PathStart)+uint64(record.PathCount) > uint64(len(d.Paths)) {
			return fmt.Errorf("invalid snapshot journey path bounds")
		}
		for _, path := range d.Paths[record.PathStart : record.PathStart+record.PathCount] {
			_, departureOK := packDepartureEntry(0, 0, path.OriginDeparture)
			_, arrivalOK := packDepartureEntry(0, 0, path.DestinationArrival)
			invalid = invalid || !departureOK || !arrivalOK
		}
		if !invalid {
			continue
		}
		journey := d.materializeJourney(journeyID(index))
		if err := ctdf.InterpolateMissingJourneyTimes(journey); err != nil {
			return fmt.Errorf("repair snapshot journey %s: %w", journey.PrimaryIdentifier, err)
		}
		offset := 0
		for origin, destination := range journey.LegCalls() {
			stored := &d.Paths[int(record.PathStart)+offset]
			stored.OriginDeparture = serviceSeconds(origin.Departure)
			stored.DestinationArrival = serviceSeconds(destination.ArrivalTime())
			if _, ok := packDepartureEntry(0, 0, stored.OriginDeparture); !ok {
				return fmt.Errorf("snapshot journey time remains out of bounds")
			}
			if _, ok := packDepartureEntry(0, 0, stored.DestinationArrival); !ok {
				return fmt.Errorf("snapshot journey time remains out of bounds")
			}
			offset++
		}
		d.Journeys[index].InitialArrival = serviceSeconds(journey.Calls[0].Arrival)
		d.Journeys[index].DepartureSeconds = serviceSeconds(journey.DepartureTime)
		repaired[journeyID(index)] = true
	}
	if len(repaired) == 0 {
		return nil
	}
	for key, entries := range d.Departures {
		kept := entries[:0]
		for _, entry := range entries {
			if !repaired[entry.journey()] {
				kept = append(kept, entry)
			}
		}
		d.Departures[key] = kept
	}
	for day, journeys := range d.DayJourneys {
		for _, id := range journeys {
			if !repaired[id] {
				continue
			}
			record := d.Journeys[id]
			for index := uint32(0); index < record.PathCount; index++ {
				path := d.Paths[record.PathStart+index]
				if path.OriginStopRef == 0 || path.OriginActivity == activitySetdown {
					continue
				}
				entry, _ := packDepartureEntry(id, index, path.OriginDeparture)
				key := bucketKey{Day: day, StopRef: path.OriginStopRef}
				d.Departures[key] = append(d.Departures[key], entry)
			}
		}
	}
	d.sortDepartureBucketsLocked()
	d.StaticRoutingReady = false
	log.Info().Int("journeys", len(repaired)).Msg("Repaired missing times in legacy departure graph snapshot")
	return nil
}
