package gtfs

import (
	"container/heap"
	stdcsv "encoding/csv"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"

	"github.com/rs/zerolog/log"
	gtfscsv "github.com/travigo/go-csv"
)

const stopTimeSortChunkSize = 100000

type sortedStopTimeGroups struct {
	chunkFiles []string
}

func newSortedStopTimeGroups(sourcePath string) (*sortedStopTimeGroups, error) {
	log.Info().Str("table", "stop_times").Msg("Processing Table")

	chunkFiles, err := writeSortedStopTimeChunks(sourcePath)
	if err != nil {
		removeStopTimeTempFiles(chunkFiles)
		return nil, err
	}

	if err := os.Remove(sourcePath); err != nil {
		log.Warn().Err(err).Str("file", sourcePath).Msg("Failed to remove stop_times temp file")
	}

	log.Info().Str("table", "stop_times").Msg("Finished Table")

	return &sortedStopTimeGroups{
		chunkFiles: chunkFiles,
	}, nil
}

func (groups *sortedStopTimeGroups) Close() {
	removeStopTimeTempFiles(groups.chunkFiles)
	groups.chunkFiles = nil
}

func (groups *sortedStopTimeGroups) Process(process func(string, []StopTime) error) error {
	readers := make([]*stopTimeChunkReader, 0, len(groups.chunkFiles))
	defer func() {
		for _, reader := range readers {
			reader.Close()
		}
	}()

	readerHeap := &stopTimeReaderHeap{}
	for index, chunkFile := range groups.chunkFiles {
		reader, ok, err := newStopTimeChunkReader(chunkFile, index)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}

		readers = append(readers, reader)
		heap.Push(readerHeap, reader)
	}

	var currentTripID string
	var hasCurrentTrip bool
	var currentTripStopTimes []StopTime

	for readerHeap.Len() > 0 {
		reader := heap.Pop(readerHeap).(*stopTimeChunkReader)
		stopTime := reader.Current

		if !hasCurrentTrip {
			currentTripID = stopTime.TripID
			hasCurrentTrip = true
		}
		if stopTime.TripID != currentTripID {
			if err := process(currentTripID, currentTripStopTimes); err != nil {
				return err
			}

			currentTripID = stopTime.TripID
			currentTripStopTimes = nil
		}

		currentTripStopTimes = append(currentTripStopTimes, stopTime)

		ok, err := reader.ReadNext()
		if err != nil {
			return err
		}
		if ok {
			heap.Push(readerHeap, reader)
		}
	}

	if len(currentTripStopTimes) > 0 {
		return process(currentTripID, currentTripStopTimes)
	}

	return nil
}

func writeSortedStopTimeChunks(sourcePath string) ([]string, error) {
	file, err := os.Open(sourcePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	decoder := gtfscsv.NewDecoder(file)

	line, err := decoder.ReadLine()
	if err != nil {
		return nil, err
	}
	if _, err = decoder.DecodeHeader(line); err != nil {
		return nil, err
	}

	chunk := make([]StopTime, 0, stopTimeSortChunkSize)
	var chunkFiles []string

	for {
		line, err = decoder.ReadLine()
		if err != nil {
			return chunkFiles, err
		}
		if line == "" {
			break
		}

		var stopTime StopTime
		if err = decoder.DecodeRecord(&stopTime, line); err != nil {
			return chunkFiles, err
		}

		chunk = append(chunk, stopTime)
		if len(chunk) == stopTimeSortChunkSize {
			chunkFile, err := writeSortedStopTimeChunk(chunk)
			if err != nil {
				return chunkFiles, err
			}

			chunkFiles = append(chunkFiles, chunkFile)
			chunk = make([]StopTime, 0, stopTimeSortChunkSize)
		}
	}

	if len(chunk) > 0 {
		chunkFile, err := writeSortedStopTimeChunk(chunk)
		if err != nil {
			return chunkFiles, err
		}

		chunkFiles = append(chunkFiles, chunkFile)
	}

	return chunkFiles, nil
}

func writeSortedStopTimeChunk(stopTimes []StopTime) (string, error) {
	sort.Slice(stopTimes, func(i, j int) bool {
		return compareStopTimes(stopTimes[i], stopTimes[j]) < 0
	})

	file, err := os.CreateTemp(os.TempDir(), "travigo-data-importer-gtfs-stop-times-")
	if err != nil {
		return "", err
	}

	writer := stdcsv.NewWriter(file)
	for _, stopTime := range stopTimes {
		if err := writer.Write([]string{
			stopTime.TripID,
			strconv.Itoa(stopTime.StopSequence),
			stopTime.ArrivalTime,
			stopTime.DepartureTime,
			stopTime.StopID,
			stopTime.StopHeadsign,
			strconv.Itoa(int(stopTime.PickupType)),
			strconv.Itoa(int(stopTime.DropOffType)),
		}); err != nil {
			file.Close()
			os.Remove(file.Name())
			return "", err
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		file.Close()
		os.Remove(file.Name())
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(file.Name())
		return "", err
	}

	return file.Name(), nil
}

type stopTimeChunkReader struct {
	File    *os.File
	Reader  *stdcsv.Reader
	Current StopTime
	Index   int
}

func newStopTimeChunkReader(path string, index int) (*stopTimeChunkReader, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}

	reader := &stopTimeChunkReader{
		File:   file,
		Reader: stdcsv.NewReader(file),
		Index:  index,
	}

	ok, err := reader.ReadNext()
	if err != nil {
		reader.Close()
		return nil, false, err
	}
	if !ok {
		reader.Close()
		return nil, false, nil
	}

	return reader, true, nil
}

func (reader *stopTimeChunkReader) ReadNext() (bool, error) {
	record, err := reader.Reader.Read()
	if err == io.EOF {
		reader.Close()
		return false, nil
	}
	if err != nil {
		return false, err
	}

	stopTime, err := parseStopTimeChunkRecord(record)
	if err != nil {
		return false, err
	}

	reader.Current = stopTime
	return true, nil
}

func (reader *stopTimeChunkReader) Close() {
	if reader.File != nil {
		reader.File.Close()
		reader.File = nil
	}
}

type stopTimeReaderHeap []*stopTimeChunkReader

func (readerHeap stopTimeReaderHeap) Len() int {
	return len(readerHeap)
}

func (readerHeap stopTimeReaderHeap) Less(i int, j int) bool {
	comparison := compareStopTimes(readerHeap[i].Current, readerHeap[j].Current)
	if comparison == 0 {
		return readerHeap[i].Index < readerHeap[j].Index
	}

	return comparison < 0
}

func (readerHeap stopTimeReaderHeap) Swap(i int, j int) {
	readerHeap[i], readerHeap[j] = readerHeap[j], readerHeap[i]
}

func (readerHeap *stopTimeReaderHeap) Push(value any) {
	*readerHeap = append(*readerHeap, value.(*stopTimeChunkReader))
}

func (readerHeap *stopTimeReaderHeap) Pop() any {
	old := *readerHeap
	lastIndex := len(old) - 1
	item := old[lastIndex]
	*readerHeap = old[:lastIndex]
	return item
}

func compareStopTimes(a StopTime, b StopTime) int {
	if a.TripID < b.TripID {
		return -1
	}
	if a.TripID > b.TripID {
		return 1
	}
	if a.StopSequence < b.StopSequence {
		return -1
	}
	if a.StopSequence > b.StopSequence {
		return 1
	}

	return 0
}

func parseStopTimeChunkRecord(record []string) (StopTime, error) {
	if len(record) != 8 {
		return StopTime{}, fmt.Errorf("stop_times chunk record has %d fields, expected 8", len(record))
	}

	stopSequence, err := parseStopTimeInt(record[1])
	if err != nil {
		return StopTime{}, err
	}
	pickupType, err := parseStopTimeInt(record[6])
	if err != nil {
		return StopTime{}, err
	}
	dropOffType, err := parseStopTimeInt(record[7])
	if err != nil {
		return StopTime{}, err
	}

	return StopTime{
		TripID:        record[0],
		StopSequence:  stopSequence,
		ArrivalTime:   record[2],
		DepartureTime: record[3],
		StopID:        record[4],
		StopHeadsign:  record[5],
		PickupType:    int8(pickupType),
		DropOffType:   int8(dropOffType),
	}, nil
}

func parseStopTimeInt(value string) (int, error) {
	if value == "" {
		return 0, nil
	}

	return strconv.Atoi(value)
}

func removeStopTimeTempFiles(files []string) {
	for _, file := range files {
		if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
			log.Warn().Err(err).Str("file", file).Msg("Failed to remove temp file")
		}
	}
}
