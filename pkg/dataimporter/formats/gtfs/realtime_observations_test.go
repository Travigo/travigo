package gtfs

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	gtfsrt "github.com/MobilityData/gtfs-realtime-bindings/golang/gtfs"
	"github.com/adjust/rmq/v5"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/realtime/vehicletracker"
	"github.com/travigo/travigo/pkg/redis_client"
	"google.golang.org/protobuf/proto"
)

type observationTestQueue struct {
	rmq.Queue
	err       error
	published vehicletracker.VehicleUpdateEvent
}

func (q *observationTestQueue) PublishBytes(payloads ...[]byte) error {
	if q.err != nil {
		return q.err
	}
	q.published = vehicletracker.VehicleUpdateEvent{}
	return json.Unmarshal(payloads[0], &q.published)
}

func TestGTFSObservationDeduplication(t *testing.T) {
	cache := new(gtfsObservationCache)
	queue := &observationTestQueue{}
	start := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)
	event := vehicletracker.VehicleUpdateEvent{
		MessageType: vehicletracker.VehicleUpdateEventTypeTrip,
		LocalID:     "trip",
		DataSource:  &ctdf.DataSourceReference{DatasetID: "feed", Timestamp: "first poll"},
		RecordedAt:  start,
		VehicleLocationUpdate: &vehicletracker.VehicleLocationUpdate{
			Timeframe:   "2026-11-15",
			StopUpdates: []vehicletracker.VehicleLocationEventStopUpdate{{StopID: "stop", ArrivalOffset: 15}},
		},
	}
	publish := func(dataset string, elapsed time.Duration, sourceTime, deduplicate, want bool) {
		t.Helper()
		now := start.Add(elapsed)
		got, err := cache.publish(queue, dataset, &event, sourceTime, deduplicate, now)
		if err != nil || got != want {
			t.Fatalf("publish at %v = %v, %v, want %v", elapsed, got, err, want)
		}
		if got && (!queue.published.SubmittedAt.Equal(now) || !queue.published.RecordedAt.Equal(event.RecordedAt) ||
			queue.published.VehicleLocationUpdate.StopUpdates[0].ArrivalOffset != event.VehicleLocationUpdate.StopUpdates[0].ArrivalOffset) {
			t.Fatalf("unexpected payload: %+v", queue.published)
		}
	}
	publish("feed", 0, false, true, true)
	event.RecordedAt = start.Add(2 * time.Minute)
	event.DataSource.Timestamp = "second poll"
	publish("feed", 2*time.Minute, false, true, false)
	publish("other feed", 2*time.Minute, false, true, true)
	event.RecordedAt = start.Add(3 * time.Minute)
	publish("feed", 3*time.Minute, false, true, true)

	// Run the remaining cases after the window has refreshed.
	start = start.Add(3 * time.Minute)

	event.VehicleLocationUpdate.StopUpdates[0].ArrivalOffset++
	publish("feed", 70*time.Second, false, true, true)
	publish("feed", 80*time.Second, false, false, true)
	publish("feed", 81*time.Second, false, false, true)
	publish("feed", 82*time.Second, false, true, true)

	publish("feed", 90*time.Second, true, true, true)
	publish("feed", 91*time.Second, true, true, false)
	event.RecordedAt = event.RecordedAt.Add(time.Second)
	publish("feed", 92*time.Second, true, true, true)

	event.VehicleLocationUpdate.StopUpdates[0].ArrivalOffset++
	queue.err = errors.New("queue unavailable")
	if written, err := cache.publish(queue, "feed", &event, true, true, start.Add(93*time.Second)); written || !errors.Is(err, queue.err) {
		t.Fatalf("failed publish = %v, %v", written, err)
	}
	queue.err = nil
	publish("feed", 94*time.Second, true, true, true)
}

type emptyObservationQueueStats struct{ rmq.Connection }

func (emptyObservationQueueStats) CollectStats([]string) (rmq.Stats, error) {
	return rmq.Stats{}, nil
}

func TestGTFSRealtimeImportInterleavedObservations(t *testing.T) {
	oldConnection := redis_client.QueueConnection
	redis_client.QueueConnection = emptyObservationQueueStats{}
	t.Cleanup(func() { redis_client.QueueConnection = oldConnection })

	dataset := datasets.DataSet{Identifier: t.Name(), LinkedDataset: "schedule"}
	dataset.SupportedObjects.RealtimeJourneys = true
	datasource := &ctdf.DataSourceReference{DatasetID: dataset.Identifier}
	queue := rmq.NewTestQueue("realtime-queue")
	now := time.Now()
	trip := &gtfsrt.TripDescriptor{TripId: proto.String("trip"), StartDate: proto.String(now.Format("20060102"))}
	position := &gtfsrt.VehiclePosition{
		Trip: trip, Timestamp: proto.Uint64(uint64(now.Unix())),
		Position: &gtfsrt.Position{Latitude: proto.Float32(51), Longitude: proto.Float32(0.1)},
	}
	malformed := &gtfsrt.VehiclePosition{
		Trip:     &gtfsrt.TripDescriptor{TripId: proto.String("bad")},
		Position: &gtfsrt.Position{Latitude: proto.Float32(float32(math.NaN())), Longitude: proto.Float32(0.1)},
	}
	feed := &gtfsrt.FeedMessage{
		Header: &gtfsrt.FeedHeader{GtfsRealtimeVersion: proto.String("2.0")},
		Entity: []*gtfsrt.FeedEntity{
			{Id: proto.String("malformed"), Vehicle: malformed},
			{Id: proto.String("position"), Vehicle: position},
			{Id: proto.String("prediction"), TripUpdate: &gtfsrt.TripUpdate{
				Trip: trip,
				StopTimeUpdate: []*gtfsrt.TripUpdate_StopTimeUpdate{{
					StopId: proto.String("stop"), Arrival: &gtfsrt.TripUpdate_StopTimeEvent{Delay: proto.Int32(60)},
				}},
			}},
		},
	}
	run := func(want int) {
		t.Helper()
		raw, err := proto.Marshal(feed)
		if err != nil {
			t.Fatal(err)
		}
		importer := Realtime{reader: bytes.NewReader(raw), queue: queue}
		if _, err = importer.Import(dataset, datasource); err != nil {
			t.Fatal(err)
		}
		if got := len(queue.LastDeliveries); got != want {
			t.Fatalf("submitted %d total updates, want %d", got, want)
		}
	}
	run(2)
	run(2)
	position.Timestamp = proto.Uint64(position.GetTimestamp() + 1)
	position.Position.Longitude = proto.Float32(0.2)
	// A newer GPS update can replace predictions, so submit them again.
	run(4)
}
