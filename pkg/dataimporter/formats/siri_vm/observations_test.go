package siri_vm

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/realtime/vehicletracker"
)

func TestObservationBatchPreservesChangesAndPublishesLatestPosition(t *testing.T) {
	now := time.Now().UTC()
	makeEvent := func(recordedAt time.Time, longitude float64, occupancy int, timestamp string) *vehicletracker.VehicleUpdateEvent {
		return &vehicletracker.VehicleUpdateEvent{
			LocalID: now.String(), SourceType: "siri-vm", MessageType: vehicletracker.VehicleUpdateEventTypeTrip,
			RecordedAt: recordedAt,
			DataSource: &ctdf.DataSourceReference{DatasetID: "feed", Timestamp: timestamp},
			VehicleLocationUpdate: &vehicletracker.VehicleLocationUpdate{
				Location:  ctdf.Location{Type: "Point", Coordinates: []float64{longitude, 51}},
				Occupancy: ctdf.RealtimeJourneyOccupancy{TotalPercentageOccupancy: occupancy},
			},
		}
	}

	batch := observationBatch{latest: make(map[observationRun]int)}
	queue := rmq.NewTestQueue("realtime")
	add := func(event *vehicletracker.VehicleUpdateEvent) {
		t.Helper()
		if err := batch.add(event); err != nil {
			t.Fatal(err)
		}
	}
	add(makeEvent(now, 1, 10, "first"))
	add(makeEvent(now.Add(time.Second), 2, 10, "first"))
	add(makeEvent(now, 1, 10, "first")) // Out of order, same observation fields.
	add(makeEvent(now.Add(2*time.Second), 3, 20, "first"))
	if err := batch.flush(queue); err != nil {
		t.Fatal(err)
	}

	// A later poll can repeat the last observation with a new import timestamp.
	add(makeEvent(now.Add(2*time.Second), 3, 20, "second"))
	if err := batch.flush(queue); err != nil {
		t.Fatal(err)
	}
	if len(queue.LastDeliveries) != 2 || batch.repeated != 1 || batch.replaced != 2 {
		t.Fatalf("published=%d repeated=%d replaced=%d, want 2, 1, 2", len(queue.LastDeliveries), batch.repeated, batch.replaced)
	}
	for i, payload := range queue.LastDeliveries {
		var event vehicletracker.VehicleUpdateEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			t.Fatal(err)
		}
		wantLongitude := float64(i + 2)
		wantOccupancy := (i + 1) * 10
		if event.SubmittedAt.IsZero() || event.DataSource.Timestamp != "first" ||
			event.VehicleLocationUpdate.Location.Coordinates[0] != wantLongitude ||
			event.VehicleLocationUpdate.Occupancy.TotalPercentageOccupancy != wantOccupancy {
			t.Fatalf("unexpected queued event: %s", payload)
		}
	}
}
