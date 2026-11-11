package batchrunner

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/dataimporter/manager"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestStageArtifactSkipsUnchangedGTFSPublisherVersion(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("GTFS version match", func(mt *mtest.T) {
		var feed bytes.Buffer
		archive := zip.NewWriter(&feed)
		info, err := archive.Create("feed_info.txt")
		if err != nil {
			mt.Fatal(err)
		}
		if _, err := io.WriteString(info, "feed_version\n2026-11\n"); err != nil {
			mt.Fatal(err)
		}
		if err := archive.Close(); err != nil {
			mt.Fatal(err)
		}
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(feed.Bytes())
		}))
		defer provider.Close()

		previousDB := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previousDB })
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".dataset_versions", mtest.FirstBatch, bson.D{
			{Key: "dataset", Value: "test-gtfs"},
			{Key: "hash", Value: "previous-archive-hash"},
			{Key: "datasetprovidedversion", Value: "2026-11"},
		}))
		dataset := &datasets.DataSet{Identifier: "test-gtfs", Format: datasets.DataSetFormatGTFSSchedule, Source: provider.URL}
		store, err := NewStore(mt.TempDir())
		if err != nil {
			mt.Fatal(err)
		}
		token, err := store.StageArtifact("test-run", func(file *os.File) (bool, string, error) {
			return manager.PrepareDatasetSource(mt.Context(), dataset, false, file)
		})
		if err != nil || token != "" {
			mt.Fatalf("unchanged GTFS version should not publish an artifact, got token %q and error %v", token, err)
		}
	})
}

func TestStagedSourceImportPreservesPublisherETagAndHash(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("publisher to PVC to child importer", func(mt *mtest.T) {
		const (
			runID    = "20261111-020000-000000000"
			oldETag  = `"previous-export"`
			newETag  = `"new-export"`
			feed     = `{"TIPLOCDATA":[]}`
			tokenEnv = "TRAVIGO_TEST_PUBLISHER_TOKEN"
		)
		mt.Setenv(tokenEnv, "publisher-secret")
		var providerRequests, artifactRequests atomic.Int32
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			providerRequests.Add(1)
			if r.Header.Get("If-None-Match") != oldETag || r.Header.Get("Authorization") != "Bearer publisher-secret" {
				http.Error(w, "missing publisher credentials or validator", http.StatusBadRequest)
				return
			}
			w.Header().Set("ETag", newETag)
			_, _ = io.WriteString(w, feed)
		}))
		defer provider.Close()

		store, err := NewStore(mt.TempDir())
		if err != nil {
			mt.Fatal(err)
		}
		serveArtifact := store.ArtifactHandler()
		artifacts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			artifactRequests.Add(1)
			if r.Header.Get("If-None-Match") != "" || r.Header.Get("Authorization") != "" {
				http.Error(w, "publisher headers sent to artifact endpoint", http.StatusBadRequest)
				return
			}
			serveArtifact.ServeHTTP(w, r)
		}))
		defer artifacts.Close()

		previousDB := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previousDB })
		dataset := &datasets.DataSet{
			Identifier:           "test-corpus",
			Format:               datasets.DataSetFormatNetworkRailCorpus,
			Source:               provider.URL,
			SourceAuthentication: &datasets.SourceAuthentication{AuthHeader: tokenEnv},
			SupportedObjects:     datasets.SupportedObjects{Stops: true},
		}
		previousVersion := bson.D{
			{Key: "dataset", Value: dataset.Identifier},
			{Key: "hash", Value: "previous-archive-hash"},
			{Key: "etag", Value: oldETag},
		}
		versionLookup := mtest.CreateCursorResponse(0, mt.DB.Name()+".dataset_versions", mtest.FirstBatch, previousVersion)
		mt.AddMockResponses(versionLookup)
		token, err := store.StageArtifact(runID, func(file *os.File) (bool, string, error) {
			return manager.PrepareDatasetSource(mt.Context(), dataset, false, file)
		})
		if err != nil || token == "" {
			mt.Fatalf("stage changed source returned token %q and error %v", token, err)
		}
		if events := mt.GetAllStartedEvents(); len(events) != 1 || events[0].CommandName != "find" {
			mt.Fatalf("source preparation should only read MongoDB, got %v", events)
		}

		mt.ClearEvents()
		mt.AddMockResponses(versionLookup,
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}),
			mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}))
		url := artifacts.URL + "/artifacts/" + runID + "/" + token
		if err := manager.ImportDatasetWithSourceOverride(mt.Context(), dataset, false, true, url); err != nil {
			mt.Fatal(err)
		}
		if providerRequests.Load() != 1 || artifactRequests.Load() != 1 {
			mt.Fatalf("wanted one publisher fetch and one artifact fetch, got %d and %d", providerRequests.Load(), artifactRequests.Load())
		}

		events := mt.GetAllStartedEvents()
		if len(events) != 3 || events[1].CommandName != "update" || events[2].CommandName != "insert" {
			mt.Fatalf("expected child importer to look up, update and report the dataset, got %v", events)
		}
		updates, err := events[1].Command.Lookup("updates").Array().Values()
		if err != nil || len(updates) != 1 {
			mt.Fatalf("expected a single version update, got %v, %v", updates, err)
		}
		version := updates[0].Document().Lookup("u", "$set").Document()
		wantHash := fmt.Sprintf("%x", sha256.Sum256([]byte(feed)))
		if version.Lookup("etag").StringValue() != newETag || version.Lookup("hash").StringValue() != wantHash {
			mt.Fatalf("version did not preserve the publisher ETag and artifact hash: %v", version)
		}
	})
}
