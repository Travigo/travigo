package departuregraph

import (
	"bufio"
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/rs/zerolog/log"
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

	buffered := bufio.NewWriterSize(temporary, 1024*1024)
	compressor, err := zstd.NewWriter(buffered, zstd.WithEncoderLevel(zstd.SpeedFastest))
	if err != nil {
		_ = temporary.Close()
		return err
	}

	data.mu.RLock()
	err = gob.NewEncoder(compressor).Encode(snapshotFile{
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
		StaticRoutingReady:     data.StaticRoutingReady,
		Journeys:               data.Journeys,
		Paths:                  data.Paths,
		Replacements:           data.Replacements,
		JourneyDays:            data.JourneyDays,
		DayJourneys:            data.DayJourneys,
		Boardings:              data.Departures,
		CompleteStops:          data.CompleteStops,
		CompleteDays:           data.CompleteDays,
		ScanDays:               data.ScanDays,
		ScanCursor:             data.ScanCursor,
		ScanProcessed:          data.ScanProcessed,
		ScanActive:             data.ScanActive,
	})
	data.mu.RUnlock()
	if err == nil {
		err = compressor.Close()
	} else {
		compressor.Close()
	}
	if err == nil {
		err = buffered.Flush()
	}
	if err == nil {
		err = temporary.Sync()
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
	if err := gob.NewDecoder(decoder).Decode(&snapshot); err != nil {
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
		StringIDs:              make(map[string]stringID, len(snapshot.Strings)),
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
		StaticRoutingReady:     snapshot.StaticRoutingReady,
		Journeys:               snapshot.Journeys,
		Paths:                  snapshot.Paths,
		Replacements:           snapshot.Replacements,
		JourneyIDs:             make(map[journeyKey]journeyID, len(snapshot.Journeys)),
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
	for index, value := range restored.Strings {
		restored.StringIDs[value] = stringID(index)
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
	if len(restored.CompleteDays) > 0 && (!restored.StaticRoutingReady || len(restored.ReverseTransferOffsets) != len(restored.Stops)+1 || len(restored.ArrivalPatternOffsets) != len(restored.Stops)+1) {
		restored.buildStaticRoutingIndexesLocked()
	}
	for key := range restored.CompleteStops {
		if value := restored.stringValue(key.StopRef); value != "" {
			restored.StopIDs[value] = key.StopRef
		}
	}
	for index, journey := range restored.Journeys {
		restored.JourneyIDs[journeyKey{PrimaryID: journey.PrimaryID}] = journeyID(index)
	}
	if len(restored.ScanDays) == 0 && len(restored.CompleteDays) > 0 {
		restored.sealBuildIndexesLocked()
	}

	g.current.Store(restored)
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
