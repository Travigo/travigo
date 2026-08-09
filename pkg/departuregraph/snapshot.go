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
	CompleteStops map[bucketKey]bool
	CompleteDays  map[dayKey]bool
}

// Save writes the current graph generation to the configured snapshot path.
// Reads can continue while the checkpoint is encoded.
func (g *Graph) Save() error {
	if g == nil || g.config.SnapshotPath == "" {
		return nil
	}
	return g.save(g.config.SnapshotPath, g.current.Load())
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
		CompleteStops: data.CompleteStops,
		CompleteDays:  data.CompleteDays,
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
	if snapshot.Version != snapshotVersion {
		return fmt.Errorf("unsupported departure graph snapshot version %d", snapshot.Version)
	}

	restored := &graphData{
		Strings:       snapshot.Strings,
		StringIDs:     make(map[string]stringID, len(snapshot.Strings)),
		Journeys:      snapshot.Journeys,
		Paths:         snapshot.Paths,
		Replacements:  snapshot.Replacements,
		JourneyIDs:    make(map[journeyKey]journeyID, len(snapshot.Journeys)),
		JourneyDays:   snapshot.JourneyDays,
		Departures:    snapshot.Departures,
		CompleteStops: snapshot.CompleteStops,
		CompleteDays:  snapshot.CompleteDays,
	}
	if len(restored.Strings) == 0 {
		restored.Strings = []string{""}
	}
	if restored.Departures == nil {
		restored.Departures = map[bucketKey][]journeyID{}
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
	for index, journey := range restored.Journeys {
		restored.JourneyIDs[journeyKey{PrimaryID: journey.PrimaryID}] = journeyID(index)
	}

	g.current.Store(restored)

	stats := restored.stats()
	log.Info().
		Str("path", path).
		Time("written_at", snapshot.WrittenAt).
		Int("journeys", stats.Journeys).
		Int("paths", stats.Paths).
		Int("departure_buckets", stats.DepartureBuckets).
		Msg("Departure graph snapshot restored")
	return nil
}
