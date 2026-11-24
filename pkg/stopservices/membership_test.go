package stopservices

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestTransferMembershipReturnsInitialSnapshot(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("initial transfer reads stable current generations", func(mt *mtest.T) {
		state := bson.D{
			{Key: "_id", Value: stateDocumentID}, {Key: "initialized", Value: true},
			{Key: "eventRevision", Value: int64(5)}, {Key: "fullRevision", Value: int64(1)},
			{Key: "fullPublishedRevision", Value: int64(1)}, {Key: "fullActiveWriters", Value: int64(0)},
			{Key: "sources", Value: bson.D{
				{Key: "a", Value: bson.D{{Key: "dataset", Value: "feed-a"}, {Key: "revision", Value: int64(2)}, {Key: "publishedRevision", Value: int64(2)}, {Key: "activeWriters", Value: int64(0)}, {Key: "generation", Value: "gen-a"}}},
				{Key: "dynamic", Value: bson.D{{Key: "dataset", Value: "gb-networkrail-vstp"}, {Key: "generation", Value: "ignored"}}},
			}},
		}
		stateResponse := func() *bson.D {
			response := mtest.CreateCursorResponse(0, mt.DB.Name()+"."+StateCollectionName, mtest.FirstBatch, state)
			return &response
		}
		mt.AddMockResponses(
			*stateResponse(),
			mtest.CreateCursorResponse(0, mt.DB.Name()+"."+RefsCollectionName, mtest.FirstBatch,
				bson.D{{Key: "_id", Value: "stop-1"}}, bson.D{{Key: "_id", Value: "stop-2"}}),
			*stateResponse(),
		)
		refs, generations, changed, active, revision, err := TransferMembership(mt.Context(), mt.DB, nil)
		if err != nil {
			mt.Fatal(err)
		}
		if !reflect.DeepEqual(refs, map[string]struct{}{"stop-1": {}, "stop-2": {}}) || !reflect.DeepEqual(generations, map[string]string{"feed-a": "gen-a"}) {
			mt.Fatalf("snapshot refs=%v generations=%v", refs, generations)
		}
		if len(changed) != 0 || !active || revision != 5 {
			mt.Fatalf("delta/status = %v/%v/%d, want empty/active/5", changed, active, revision)
		}
	})
}

func TestTransferMembershipReturnsNetGlobalChanges(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("overlapping references retain global presence", func(mt *mtest.T) {
		state := bson.D{{Key: "_id", Value: stateDocumentID}, {Key: "initialized", Value: true},
			{Key: "eventRevision", Value: int64(9)}, {Key: "fullRevision", Value: int64(2)},
			{Key: "fullPublishedRevision", Value: int64(2)}, {Key: "fullActiveWriters", Value: int64(0)},
			{Key: "retired", Value: bson.A{bson.D{{Key: "dataset", Value: "feed-a"}, {Key: "generation", Value: "gen-1"}, {Key: "retiredAt", Value: time.Now().UTC()}}}},
			{Key: "sources", Value: bson.D{{Key: "a", Value: bson.D{
				{Key: "dataset", Value: "feed-a"}, {Key: "revision", Value: int64(3)},
				{Key: "publishedRevision", Value: int64(3)}, {Key: "activeWriters", Value: int64(0)},
				{Key: "generation", Value: "gen-2"},
			}}}}}
		stateResponse := func() *bson.D {
			response := mtest.CreateCursorResponse(0, mt.DB.Name()+"."+StateCollectionName, mtest.FirstBatch, state)
			return &response
		}
		refResponse := func(docs ...bson.D) *bson.D {
			response := mtest.CreateCursorResponse(0, mt.DB.Name()+"."+RefsCollectionName, mtest.FirstBatch, docs...)
			return &response
		}
		mt.AddMockResponses(
			*stateResponse(),
			*refResponse(bson.D{{Key: "_id", Value: "removed"}}, bson.D{{Key: "_id", Value: "shared"}}),                                     // old generation membership
			*refResponse(bson.D{{Key: "_id", Value: "shared"}}, bson.D{{Key: "_id", Value: "added"}}),                                       // new generation membership
			*refResponse(bson.D{{Key: "_id", Value: "shared"}}, bson.D{{Key: "_id", Value: "added"}}),                                       // current candidates
			*refResponse(bson.D{{Key: "_id", Value: "removed"}}, bson.D{{Key: "_id", Value: "shared"}}),                                     // previous candidates
			mtest.CreateCursorResponse(0, mt.DB.Name()+"."+RefsCollectionName, mtest.FirstBatch, bson.D{{Key: "stopref", Value: "shared"}}), // active existence
			*stateResponse(),
		)
		refs, generations, changed, active, revision, err := TransferMembership(mt.Context(), mt.DB, map[string]string{"feed-a": "gen-1"})
		if err != nil {
			mt.Fatal(err)
		}
		if refs != nil || !reflect.DeepEqual(generations, map[string]string{"feed-a": "gen-2"}) {
			mt.Fatalf("incremental result refs=%v generations=%v", refs, generations)
		}
		if !reflect.DeepEqual(changed, map[string]bool{"removed": false, "added": true}) || !active || revision != 9 {
			mt.Fatalf("delta/status = %v/%v/%d", changed, active, revision)
		}
	})
}

func TestTransferMembershipSkipsUnchangedGenerationScans(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("unchanged datasets do not load generation membership", func(mt *mtest.T) {
		state := bson.D{{Key: "_id", Value: stateDocumentID}, {Key: "initialized", Value: true},
			{Key: "eventRevision", Value: int64(4)}, {Key: "fullRevision", Value: int64(1)},
			{Key: "fullPublishedRevision", Value: int64(1)}, {Key: "fullActiveWriters", Value: int64(0)},
			{Key: "sources", Value: bson.D{{Key: "large", Value: bson.D{{Key: "dataset", Value: "large-feed"}, {Key: "revision", Value: int64(1)}, {Key: "publishedRevision", Value: int64(1)}, {Key: "activeWriters", Value: int64(0)}, {Key: "generation", Value: "gen-a"}}}}}}
		stateResponse := func() *bson.D {
			response := mtest.CreateCursorResponse(0, mt.DB.Name()+"."+StateCollectionName, mtest.FirstBatch, state)
			return &response
		}
		mt.AddMockResponses(*stateResponse(), mtest.CreateCursorResponse(0, mt.DB.Name()+"."+RefsCollectionName, mtest.FirstBatch, bson.D{{Key: "stopref", Value: "present"}}), *stateResponse())
		refs, _, changed, active, _, err := TransferMembership(mt.Context(), mt.DB, map[string]string{"large-feed": "gen-a"})
		if err != nil || refs != nil || len(changed) != 0 || !active {
			mt.Fatalf("transfer result = refs:%v changed:%v active:%v err:%v", refs, changed, active, err)
		}
		for _, event := range mt.GetAllStartedEvents() {
			if event.CommandName == "aggregate" {
				mt.Fatalf("unchanged generation unexpectedly scanned: %#v", event.Command)
			}
		}
	})
}

func TestTransferMembershipFallsBackForPrunedRetiredGeneration(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("missing retired metadata triggers full current snapshot", func(mt *mtest.T) {
		state := bson.D{{Key: "_id", Value: stateDocumentID}, {Key: "initialized", Value: true},
			{Key: "eventRevision", Value: int64(6)}, {Key: "fullRevision", Value: int64(2)},
			{Key: "fullPublishedRevision", Value: int64(2)}, {Key: "fullActiveWriters", Value: int64(0)},
			{Key: "sources", Value: bson.D{{Key: "a", Value: bson.D{{Key: "dataset", Value: "feed-a"}, {Key: "revision", Value: int64(2)}, {Key: "publishedRevision", Value: int64(2)}, {Key: "activeWriters", Value: int64(0)}, {Key: "generation", Value: "gen-2"}}}}}}
		stateResponse := func() *bson.D {
			response := mtest.CreateCursorResponse(0, mt.DB.Name()+"."+StateCollectionName, mtest.FirstBatch, state)
			return &response
		}
		mt.AddMockResponses(*stateResponse(), mtest.CreateCursorResponse(0, mt.DB.Name()+"."+RefsCollectionName, mtest.FirstBatch, bson.D{{Key: "_id", Value: "current-stop"}}), *stateResponse())
		refs, generations, changed, _, _, err := TransferMembership(mt.Context(), mt.DB, map[string]string{"feed-a": "gen-1"})
		if err != nil || !reflect.DeepEqual(refs, map[string]struct{}{"current-stop": {}}) || generations["feed-a"] != "gen-2" || len(changed) != 0 {
			mt.Fatalf("fallback snapshot = refs:%v generations:%v changed:%v err:%v", refs, generations, changed, err)
		}
	})
}

func TestTransferMembershipRejectsUnstableSnapshot(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("source revision changes after read", func(mt *mtest.T) {
		state := func(revision int64) *bson.D {
			doc := bson.D{{Key: "_id", Value: stateDocumentID}, {Key: "initialized", Value: true}, {Key: "eventRevision", Value: revision},
				{Key: "fullRevision", Value: int64(1)}, {Key: "fullPublishedRevision", Value: int64(1)}, {Key: "fullActiveWriters", Value: int64(0)}, {Key: "sources", Value: bson.D{}}}
			response := mtest.CreateCursorResponse(0, mt.DB.Name()+"."+StateCollectionName, mtest.FirstBatch, doc)
			return &response
		}
		mt.AddMockResponses(*state(2), *state(3))
		_, _, _, _, _, err := TransferMembership(mt.Context(), mt.DB, nil)
		if !errors.Is(err, ErrSourceChanged) {
			mt.Fatalf("error = %v, want ErrSourceChanged", err)
		}
	})
}
