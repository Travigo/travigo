package indexer

import (
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/elastic_client"
	"github.com/travigo/travigo/pkg/stopservices"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestRetiredStopIndexRebuildWaitsForPublication(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, test := range []struct {
		name         string
		linkers      bson.A
		services     bson.D
		wantCommands int
		wantError    string
	}{
		{
			name: "link failed", linkers: bson.A{"linker-services", "linker-journeys"},
			wantCommands: 2, wantError: "link stops, services and journeys",
		},
		{
			name: "stop services stale", linkers: bson.A{"linker-stops", "linker-services", "linker-journeys"},
			services:     bson.D{{Key: "initialized", Value: true}, {Key: "fullRevision", Value: 2}, {Key: "fullPublishedRevision", Value: 1}},
			wantCommands: 3, wantError: "refresh stop services",
		},
		{
			name: "ready", linkers: bson.A{"linker-stops", "linker-services", "linker-journeys"},
			services: bson.D{{Key: "initialized", Value: true}}, wantCommands: 5,
		},
	} {
		mt.Run(test.name, func(mt *mtest.T) {
			previousDB := database.Instance
			database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
			mt.Cleanup(func() { database.Instance = previousDB })

			rebuildAfter := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
			mt.AddMockResponses(
				mtest.CreateCursorResponse(0, mt.DB.Name()+".dataset_versions", mtest.FirstBatch,
					bson.D{{Key: "stopindexrebuildafter", Value: rebuildAfter}}),
				mtest.CreateSuccessResponse(bson.E{Key: "values", Value: test.linkers}),
			)
			if test.services != nil {
				mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+stopservices.StateCollectionName,
					mtest.FirstBatch, test.services))
			}
			if test.wantError == "" {
				success := mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1})
				mt.AddMockResponses(success, success)
			}
			err := invalidateRetiredStopIndexes(mt.Context(), mt.DB.Collection("dataset_versions"))
			if test.wantError == "" && err != nil ||
				test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				mt.Fatalf("unexpected readiness result: %v", err)
			}
			events := mt.GetAllStartedEvents()
			if len(events) != test.wantCommands {
				mt.Fatalf("got %d commands, want %d", len(events), test.wantCommands)
			}
			if test.wantError == "" {
				checkpoints := events[3].Command.Lookup("updates", "0", "u").Document()
				request := events[4].Command.Lookup("updates", "0").Document()
				if checkpoints.Lookup("$unset", "indexedstopsversion").Type == 0 ||
					!request.Lookup("q", "stopindexrebuildafter").Time().Equal(rebuildAfter) ||
					request.Lookup("u", "$unset", "stopindexrebuildafter").Type == 0 {
					mt.Fatalf("expected invalidation followed by clearing the same request, got %s then %s", checkpoints, request)
				}
			}
		})
	}
}

func TestStopDatasetIndexingResumesAfterBulkFailure(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("retry skips a completed dataset and rebuilds the partial index", func(mt *mtest.T) {
		previousDB, previousElastic, previousAggregator := database.Instance, elastic_client.Client, dataaggregator.GlobalAggregator
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		dataaggregator.GlobalAggregator = dataaggregator.Aggregator{}
		serviceBatchLookups := 0
		dataaggregator.GlobalAggregator.RegisterSource(stopIndexTestServices{batchLookups: &serviceBatchLookups})
		mt.Cleanup(func() {
			database.Instance, elastic_client.Client, dataaggregator.GlobalAggregator = previousDB, previousElastic, previousAggregator
		})

		type elasticResponse struct {
			request string
			status  int
			body    string
		}
		build := func(dataset, bulkResponse string, refresh bool) []elasticResponse {
			index := "/" + stopDatasetIndexName(dataset)
			responses := []elasticResponse{
				{"DELETE " + index, 404, "{}"},
				{"PUT " + index, 200, `{"acknowledged":true}`},
				{"POST " + index + "/_bulk", 200, bulkResponse},
			}
			if refresh {
				responses = append(responses, elasticResponse{"POST " + index + "/_refresh", 200, `{"_shards":{"successful":1,"failed":0}}`})
			}
			return responses
		}
		ok := `{"items":[{"index":{"status":201}}]}`
		rejected := `{"errors":true,"items":[{"index":{"status":400,"error":{"type":"mapper_parsing_exception","reason":"bad stop"}}}]}`
		responses := append(build("a", ok, true), elasticResponse{"HEAD /travigo-stops-b", 404, ""})
		responses = append(responses, build("b", rejected, false)...)
		responses = append(responses, elasticResponse{"HEAD /travigo-stops-a", 200, ""})
		responses = append(responses, build("b", ok, true)...)
		elastic, err := elasticsearch.NewClient(elasticsearch.Config{
			Addresses:    []string{"http://elasticsearch.test"},
			DisableRetry: true,
			Transport: stopIndexTestTransport(func(r *http.Request) (*http.Response, error) {
				request := r.Method + " " + r.URL.Path
				if len(responses) == 0 || request != responses[0].request {
					mt.Errorf("unexpected Elasticsearch request %s, remaining responses %v", request, responses)
					return nil, fmt.Errorf("unexpected Elasticsearch request %s", request)
				}
				reply := responses[0]
				responses = responses[1:]
				return &http.Response{
					StatusCode: reply.status,
					Header:     http.Header{"X-Elastic-Product": {"Elasticsearch"}},
					Body:       io.NopCloser(strings.NewReader(reply.body)),
				}, nil
			}),
		})
		if err != nil {
			mt.Fatal(err)
		}
		elastic_client.Client = elastic

		importedAt := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
		versionReply := func(dataset string, indexed bool) bson.D {
			version := bson.D{{Key: "dataset", Value: dataset}, {Key: "lastmodified", Value: importedAt}}
			if indexed {
				version = append(version, bson.E{Key: "indexedstopsversion", Value: importedAt})
			}
			return mtest.CreateCursorResponse(0, mt.DB.Name()+".dataset_versions", mtest.FirstBatch, version)
		}
		stopReply := mtest.CreateCursorResponse(0, mt.DB.Name()+".stops", mtest.FirstBatch, bson.D{
			{Key: "primaryidentifier", Value: "stop-1"}, {Key: "active", Value: true},
		})
		updateReply := mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1})
		mt.AddMockResponses(
			versionReply("a", false), stopReply, updateReply,
			versionReply("b", true), updateReply, stopReply,
			versionReply("a", true),
			versionReply("b", false), stopReply, updateReply,
		)
		versions := mt.DB.Collection("dataset_versions")

		if err := indexStopDataset(mt.Context(), versions, "a"); err != nil {
			mt.Fatal(err)
		}
		if err := indexStopDataset(mt.Context(), versions, "b"); err == nil || !strings.Contains(err.Error(), "bulk writes incomplete") {
			mt.Fatalf("expected bulk write failure, got %v", err)
		}
		for _, dataset := range []string{"a", "b"} {
			if err := indexStopDataset(mt.Context(), versions, dataset); err != nil {
				mt.Fatal(err)
			}
		}
		if len(responses) != 0 {
			mt.Fatalf("unused Elasticsearch responses: %v", responses)
		}
		if serviceBatchLookups != 3 {
			mt.Fatalf("stop service batch lookups = %d, want one per rebuild", serviceBatchLookups)
		}

		var updates []string
		for _, event := range mt.GetAllStartedEvents() {
			if event.CommandName != "update" {
				continue
			}
			values, err := event.Command.Lookup("updates").Array().Values()
			if err != nil || len(values) != 1 {
				mt.Fatalf("invalid version update: %v, %v", values, err)
			}
			update := values[0].Document()
			dataset := update.Lookup("q", "dataset").StringValue()
			if update.Lookup("u", "$unset", "indexedstopsversion").Type != 0 {
				updates = append(updates, "clear "+dataset)
				continue
			}
			if got := update.Lookup("u", "$max", "indexedstopsversion").Time(); !got.Equal(importedAt) {
				mt.Fatalf("checkpointed %s at %s, expected %s", dataset, got, importedAt)
			}
			updates = append(updates, "save "+dataset)
		}
		if want := []string{"save a", "clear b", "save b"}; !reflect.DeepEqual(updates, want) {
			mt.Fatalf("checkpoint updates = %v, expected %v", updates, want)
		}
	})
}

type stopIndexTestTransport func(*http.Request) (*http.Response, error)

func (transport stopIndexTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

type stopIndexTestServices struct {
	batchLookups *int
}

func (stopIndexTestServices) GetName() string { return "test services" }

func (stopIndexTestServices) Supports() []reflect.Type {
	return []reflect.Type{reflect.TypeFor[[]*ctdf.Service](), reflect.TypeFor[map[string][]*ctdf.Service]()}
}

func (source stopIndexTestServices) Lookup(value any) (any, error) {
	switch q := value.(type) {
	case query.ServicesByStops:
		if source.batchLookups != nil {
			*source.batchLookups = *source.batchLookups + 1
		}
		servicesByStop := make(map[string][]*ctdf.Service, len(q.Stops))
		for _, stop := range q.Stops {
			if stop != nil {
				servicesByStop[stop.PrimaryIdentifier] = []*ctdf.Service{}
			}
		}
		return servicesByStop, nil
	case query.ServicesByStop:
		return []*ctdf.Service{}, nil
	default:
		return nil, fmt.Errorf("unexpected stop service query %T", value)
	}
}
