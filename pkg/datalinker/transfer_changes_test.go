package datalinker

import (
	"context"
	"github.com/travigo/travigo/pkg/ctdf"
	"reflect"
	"strings"
	"testing"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestUniqueSortedStrings(t *testing.T) {
	got := uniqueSortedStrings([]string{"stop-b", "", "stop-a", "stop-b"})
	want := []string{"stop-a", "stop-b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("uniqueSortedStrings() = %v, want %v", got, want)
	}
}

func TestReadLinkedStopChangesReturnsFullBarrierOnStaleLiveToken(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("stale live generation", func(mt *mtest.T) {
		previous := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		defer func() { database.Instance = previous }()
		mt.AddMockResponses(
			findOneResponse(mt, "linked_stop_change_state", bson.M{"_id": linkedStopChangeStateID, "building": false, "revision": "r2", "livetoken": "new", "liveempty": false}),
			findOneResponse(mt, "stops", bson.M{"_linkerpublicationtoken": "old"}),
		)
		ids, revision, full, err := ReadLinkedStopChanges(context.Background(), "r2")
		if err != nil {
			mt.Fatalf("ReadLinkedStopChanges: %v", err)
		}
		if !full || ids != nil {
			mt.Fatalf("expected full scan with no IDs, got ids=%v full=%v", ids, full)
		}
		if revision != "r2:live-mismatch:old" {
			mt.Fatalf("revision = %q", revision)
		}
	})
}

func TestBeginLinkedStopChangePublicationUsesTokenScopedStaging(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("first publication", func(mt *mtest.T) {
		previous := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		defer func() { database.Instance = previous }()
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, "travigo.linked_stop_change_state", mtest.FirstBatch),
			mtest.CreateSuccessResponse(bson.E{Key: "ok", Value: 1}, bson.E{Key: "n", Value: 1}),
		)
		token, staging, err := beginLinkedStopChangePublication(context.Background())
		if err != nil {
			mt.Fatalf("begin publication: %v", err)
		}
		if len(token) != 32 || staging != "stops_staging_"+token {
			mt.Fatalf("token/staging mismatch: %q %q", token, staging)
		}
		if strings.Contains(staging, "_staging_stops") {
			mt.Fatalf("unexpected shared staging name: %q", staging)
		}
	})
}

func TestTransferStopDigestUsesOnlyTransferInputs(t *testing.T) {
	stop := integrationStop("stop", .12)
	stop.OtherIdentifiers = []string{"b", "a"}
	stop.TransportTypes = []ctdf.TransportType{ctdf.TransportTypeRail}
	want, err := linkedTransferDigest(&stop)
	if err != nil {
		t.Fatal(err)
	}
	stop.OtherIdentifiers = []string{"a", "b", "b"}
	stop.PrimaryName = "new display name"
	stop.TransportTypes = append(stop.TransportTypes, ctdf.TransportTypeBus)
	got, err := linkedTransferDigest(&stop)
	if err != nil || got != want {
		t.Fatalf("irrelevant edit changed digest, err=%v", err)
	}
	stop.OtherIdentifiers = append(stop.OtherIdentifiers, "new-service-alias")
	got, err = linkedTransferDigest(&stop)
	if err != nil || got == want {
		t.Fatalf("alias edit did not change digest, err=%v", err)
	}
}

func TestLinkedStopLiveGenerationValidation(t *testing.T) {
	tests := []struct {
		name          string
		expected      string
		observed      string
		found         bool
		expectedEmpty bool
		want          bool
	}{
		{name: "matching token", expected: "new", observed: "new", found: true, want: true},
		{name: "stale publisher token", expected: "new", observed: "old", found: true, want: false},
		{name: "legacy live document", expected: "new", found: true, want: false},
		{name: "expected empty collection", expected: "new", expectedEmpty: true, want: true},
		{name: "unexpected empty collection", expected: "new", want: false},
		{name: "legacy state", found: true, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := linkedStopLiveGenerationMatches(test.expected, test.observed, test.found, test.expectedEmpty); got != test.want {
				t.Fatalf("linkedStopLiveGenerationMatches() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestLinkedStopMismatchRevisionIsDistinct(t *testing.T) {
	got := linkedStopMismatchRevision("r1", "old-token")
	if got == "r1" || got != "r1:live-mismatch:old-token" {
		t.Fatalf("mismatch revision = %q", got)
	}
	if got := linkedStopMismatchRevision("r1", ""); got != "r1:live-mismatch:legacy" {
		t.Fatalf("legacy mismatch revision = %q", got)
	}
}
