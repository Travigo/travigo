package departuregraph

import (
	"bytes"
	"context"
	"encoding/gob"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/travigo/travigo/pkg/ctdf"
)

type snapshotWriteCounter struct{ total, maximum int }

func (w *snapshotWriteCounter) Write(p []byte) (int, error) {
	w.total += len(p)
	w.maximum = max(w.maximum, len(p))
	return len(p), nil
}

func TestSnapshotMessagesRemainBoundedForLargeArraysAndBuckets(t *testing.T) {
	snapshot := snapshotFile{Version: snapshotVersion, Paths: make([]pathRecord, 100000), Boardings: map[bucketKey][]departureEntry{
		{Day: 20261102, StopRef: 1}: make([]departureEntry, 100000),
	}, StopGrid: map[spatialCell][]uint32{
		{Longitude: 1, Latitude: 1}: {1, 2},
		{}:                          nil,
		{Longitude: -1}:             {3},
		{Latitude: 2}:               {},
	}}
	for i := range snapshot.Paths {
		snapshot.Paths[i] = pathRecord{OriginStopRef: 1000000, DestinationStopRef: 1000001, OriginDeparture: 90000, DestinationArrival: 91000}
		snapshot.Boardings[bucketKey{Day: 20261102, StopRef: 1}][i] = departureEntry(1<<60 | uint64(i))
	}
	counter := &snapshotWriteCounter{}
	if err := encodeSnapshot(counter, snapshot); err != nil {
		t.Fatal(err)
	}
	if counter.total < 2*1024*1024 || counter.maximum > 512*1024 {
		t.Fatalf("unexpected gob buffering: total=%d largest=%d", counter.total, counter.maximum)
	}
	var encoded bytes.Buffer
	if err := encodeSnapshot(&encoded, snapshot); err != nil {
		t.Fatal(err)
	}
	var restored snapshotFile
	if err := decodeSnapshot(&encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snapshot, restored) {
		t.Fatal("chunked snapshot changed array or bucket contents")
	}
}

func TestSnapshotReaderAcceptsLegacyAndRejectsTruncation(t *testing.T) {
	legacy := snapshotFile{Version: 8, Strings: []string{"", "stop"}, Boardings: map[bucketKey][]departureEntry{}}
	var encoded bytes.Buffer
	if err := gob.NewEncoder(&encoded).Encode(legacy); err != nil {
		t.Fatal(err)
	}
	var restored snapshotFile
	if err := decodeSnapshot(&encoded, &restored); err != nil || !reflect.DeepEqual(legacy, restored) {
		t.Fatalf("legacy restore: %v", err)
	}
	var current bytes.Buffer
	if err := encodeSnapshot(&current, snapshotFile{Version: snapshotVersion, Strings: []string{"stop"}}); err != nil {
		t.Fatal(err)
	}
	for _, length := range []int{current.Len() / 2, current.Len() - 1} {
		if err := decodeSnapshot(bytes.NewReader(current.Bytes()[:length]), &snapshotFile{}); err == nil {
			t.Fatal("truncated snapshot accepted")
		}
	}
}

type pausedSnapshotWriter struct {
	bytes.Buffer
	started, resume chan struct{}
	once            sync.Once
}

func (w *pausedSnapshotWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started); <-w.resume })
	return w.Buffer.Write(p)
}

func TestCheckpointIOAllowsAppendsAndReadersButProtectsCompaction(t *testing.T) {
	data := newGraphData()
	day := dayKey(20261102)
	data.addJourney(day, testJourney())
	writer := &pausedSnapshotWriter{started: make(chan struct{}), resume: make(chan struct{})}
	var release sync.Once
	unblock := func() { release.Do(func() { close(writer.resume) }) }
	defer unblock()
	saved := make(chan error, 1)
	go func() { saved <- data.writeSnapshot(writer) }()
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("checkpoint did not start")
	}
	appended := make(chan struct{})
	go func() { data.addJourney(day, testJourneyWithID("journey-2")); close(appended) }()
	select {
	case <-appended:
	case <-time.After(time.Second):
		t.Fatal("checkpoint I/O blocked append")
	}
	compacting := make(chan struct{})
	go func() { data.completeScan([]time.Time{dayKeyDate(day, time.UTC)}); close(compacting) }()
	read := make(chan int, 1)
	go func() { read <- len(data.materializeStop(day, []string{"stop-a"})) }()
	select {
	case count := <-read:
		if count != 2 {
			t.Fatalf("got %d journeys", count)
		}
	case <-time.After(time.Second):
		t.Fatal("maintenance waiting for checkpoint blocked readers")
	}
	select {
	case <-compacting:
		t.Fatal("compaction mutated a checkpoint's array prefixes")
	default:
	}
	unblock()
	if err := <-saved; err != nil {
		t.Fatal(err)
	}
	select {
	case <-compacting:
	case <-time.After(time.Second):
		t.Fatal("compaction did not resume")
	}
	var snapshot snapshotFile
	if err := decodeSnapshot(&writer.Buffer, &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Journeys) != 1 || len(snapshot.DayJourneys[day]) != 1 {
		t.Fatal("checkpoint mixed data from before and after capture")
	}
	for _, entries := range snapshot.Boardings {
		if len(entries) != 1 || entries[0].journey() != 0 {
			t.Fatal("bucket prefix changed after capture")
		}
	}
}

func TestLegacySnapshotRepairsMissingTimesAndRewritesFormat(t *testing.T) {
	graph := New(nil, Config{Enabled: true})
	data := graph.current.Load()
	if err := data.loadTopology(context.Background(), planningTopologyLoader{stops: []*ctdf.Stop{
		{PrimaryIdentifier: "stop-a"}, {PrimaryIdentifier: "stop-b"}, {PrimaryIdentifier: "stop-c"},
	}}); err != nil {
		t.Fatal(err)
	}
	day := dayKey(20261102)
	data.addJourney(day, testJourney())
	data.completeScan([]time.Time{dayKeyDate(day, time.UTC)})
	var current bytes.Buffer
	if err := data.writeSnapshot(&current); err != nil {
		t.Fatal(err)
	}
	var legacy snapshotFile
	if err := decodeSnapshot(&current, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy.Version = 8
	legacy.Paths[0].DestinationArrival = 31622400
	legacy.Paths[1].OriginDeparture = 31622400
	// The old graph omitted boarding at the untimed intermediate stop.
	delete(legacy.Boardings, bucketKey{Day: day, StopRef: legacy.Paths[1].OriginStopRef})
	path := filepath.Join(t.TempDir(), "snapshot.zst")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := zstd.NewWriter(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := gob.NewEncoder(compressed).Encode(legacy); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	restored := New(nil, Config{Enabled: true, SnapshotPath: path})
	if err := restored.restore(path); err != nil {
		t.Fatal(err)
	}
	journeys := restored.current.Load().materializeStop(day, []string{"stop-b"})
	if len(journeys) != 1 || journeys[0].Calls[1].Departure.IsZero() {
		t.Fatal("legacy untimed stop was not repaired and reindexed")
	}
	if err := restored.Save(); err != nil {
		t.Fatal(err)
	}
	if restored.Stats().Snapshot.SuccessfulWrites != 1 {
		t.Fatal("legacy checkpoint was not rewritten")
	}
	file, err = os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := zstd.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var updated snapshotFile
	if err := decodeSnapshot(reader, &updated); err != nil || updated.Version != snapshotVersion {
		t.Fatalf("updated format: %v", err)
	}
}

func BenchmarkChunkedSnapshot(b *testing.B) {
	snapshot := snapshotFile{Version: snapshotVersion, Paths: make([]pathRecord, 100000)}
	for i := range snapshot.Paths {
		snapshot.Paths[i] = pathRecord{OriginStopRef: 10000, DestinationStopRef: 20000, OriginDeparture: 90000, DestinationArrival: 91000}
	}
	for _, chunked := range []bool{false, true} {
		name := "legacy"
		if chunked {
			name = "chunked"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var err error
				if chunked {
					err = encodeSnapshot(io.Discard, snapshot)
				} else {
					err = gob.NewEncoder(io.Discard).Encode(snapshot)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
