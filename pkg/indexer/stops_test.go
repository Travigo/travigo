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
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/elastic_client"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestStopDatasetIndexingResumesAfterBulkFailure(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("retry skips a completed dataset and rebuilds the partial index", func(mt *mtest.T) {
		previousDB, previousElastic, previousAggregator := database.Instance, elastic_client.Client, dataaggregator.GlobalAggregator
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		dataaggregator.GlobalAggregator = dataaggregator.Aggregator{}
		dataaggregator.GlobalAggregator.RegisterSource(stopIndexTestServices{})
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

type stopIndexTestServices struct{}

func (stopIndexTestServices) GetName() string { return "test services" }

func (stopIndexTestServices) Supports() []reflect.Type {
	return []reflect.Type{reflect.TypeFor[[]*ctdf.Service]()}
}

func (stopIndexTestServices) Lookup(any) (any, error) {
	return []*ctdf.Service{}, nil
}
