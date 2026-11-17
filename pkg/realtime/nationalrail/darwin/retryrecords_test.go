package darwin

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

func TestDedupeDarwinFormationRetriesKeepsNewestPerRun(t *testing.T) {
	first := darwinFormationRetryRecord{
		ID:               primitive.NewObjectID(),
		CreationDateTime: time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC),
		Record:           ScheduleFormations{RID: "rid-1", Formations: []Formation{{FID: "first"}}},
	}
	second := darwinFormationRetryRecord{
		ID:               primitive.NewObjectID(),
		CreationDateTime: time.Date(2026, 11, 17, 10, 1, 0, 0, time.UTC),
		Record:           ScheduleFormations{RID: "rid-2", Formations: []Formation{{FID: "other"}}},
	}
	third := darwinFormationRetryRecord{
		ID:               primitive.NewObjectID(),
		CreationDateTime: time.Date(2026, 11, 17, 10, 2, 0, 0, time.UTC),
		Record:           ScheduleFormations{RID: "rid-1", Formations: []Formation{{FID: "latest"}}},
	}
	missingRID := darwinFormationRetryRecord{
		ID:               primitive.NewObjectID(),
		CreationDateTime: time.Date(2026, 11, 17, 10, 3, 0, 0, time.UTC),
		Record:           ScheduleFormations{Formations: []Formation{{FID: "no-rid"}}},
	}
	missingFormations := darwinFormationRetryRecord{
		ID:               primitive.NewObjectID(),
		CreationDateTime: time.Date(2026, 11, 17, 10, 4, 0, 0, time.UTC),
		Record:           ScheduleFormations{RID: "rid-empty"},
	}

	retries := dedupeDarwinFormationRetries([]darwinFormationRetryRecord{first, second, third, missingRID, missingFormations})

	if len(retries) != 2 {
		t.Fatalf("got %d retries, want 2", len(retries))
	}

	if retries[0].record.Record.RID != "rid-1" {
		t.Fatalf("first retry is %q, want rid-1", retries[0].record.Record.RID)
	}
	if retries[0].record.Record.Formations[0].FID != "latest" {
		t.Fatalf("first retry kept formation %q, want latest", retries[0].record.Record.Formations[0].FID)
	}
	if len(retries[0].superseded) != 1 || retries[0].superseded[0] != first.ID {
		t.Fatalf("first retry superseded = %v, want [%s]", retries[0].superseded, first.ID.Hex())
	}

	if retries[1].record.Record.RID != "rid-2" {
		t.Fatalf("second retry is %q, want rid-2", retries[1].record.Record.RID)
	}
}
