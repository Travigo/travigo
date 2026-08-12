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
	Version       int
	WrittenAt     time.Time
	Strings       []string
	Journeys      []journeyRecord
	Paths         []pathRecord
	Replacements  []stringID
	JourneyDays   map[journeyDayKey]bool
	Departures    map[bucketKey][]journeyID
	Arrivals      map[bucketKey][]journeyID
	CompleteStops map[bucketKey]bool
	CompleteDays  map[dayKey]bool
	ScanDays      []dayKey
	ScanCursor    string
	ScanProcessed int64
	ScanActive    int64
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
	started := g.metrics.snapshot.beginWrite()
	err := g.save(path, data)
	var size int64
	if info, statErr := os.Stat(path); statErr == nil {
		size = info.Size()
	}
	g.metrics.snapshot.finishWrite(started, size, err)
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
		Version:       snapshotVersion,
		WrittenAt:     time.Now(),
		Strings:       data.Strings,
		Journeys:      data.Journeys,
		Paths:         data.Paths,
		Replacements:  data.Replacements,
		JourneyDays:   data.JourneyDays,
		Departures:    data.Departures,
		Arrivals:      data.Arrivals,
		CompleteStops: data.CompleteStops,
		CompleteDays:  data.CompleteDays,
		ScanDays:      data.ScanDays,
		ScanCursor:    data.ScanCursor,
		ScanProcessed: data.ScanProcessed,
		ScanActive:    data.ScanActive,
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

	restored := &graphData{
		Strings:       snapshot.Strings,
		StringIDs:     make(map[string]stringID, len(snapshot.Strings)),
		StopIDs:       map[string]stringID{"": 0},
		Journeys:      snapshot.Journeys,
		Paths:         snapshot.Paths,
		Replacements:  snapshot.Replacements,
		JourneyIDs:    make(map[journeyKey]journeyID, len(snapshot.Journeys)),
		JourneyDays:   snapshot.JourneyDays,
		Departures:    snapshot.Departures,
		Arrivals:      snapshot.Arrivals,
		CompleteStops: snapshot.CompleteStops,
		CompleteDays:  snapshot.CompleteDays,
		ScanDays:      snapshot.ScanDays,
		ScanCursor:    snapshot.ScanCursor,
		ScanProcessed: snapshot.ScanProcessed,
		ScanActive:    snapshot.ScanActive,
	}
	if len(restored.Strings) == 0 {
		restored.Strings = []string{""}
	}
	if restored.Departures == nil {
		restored.Departures = map[bucketKey][]journeyID{}
	}
	if restored.Arrivals == nil {
		restored.Arrivals = map[bucketKey][]journeyID{}
		for active := range restored.JourneyDays {
			if int(active.Journey) >= len(restored.Journeys) {
				continue
			}
			record := restored.Journeys[active.Journey]
			for index := uint32(0); index < record.PathCount; index++ {
				path := restored.Paths[record.PathStart+index]
				if path.DestinationStopRef == 0 {
					continue
				}
				key := bucketKey{Day: active.Day, StopRef: path.DestinationStopRef}
				restored.Arrivals[key] = append(restored.Arrivals[key], active.Journey)
			}
		}
	}
	if restored.JourneyDays == nil {
		restored.JourneyDays = map[journeyDayKey]bool{}
	}
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
