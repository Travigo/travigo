package databaselookup

import (
	"reflect"
	"sort"
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestMaterializedServicesByStopsCombinesAliasesPlatformsAndCanonicalServices(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("all stop refs and service refs are queried in batches", func(mt *mtest.T) {
		previousDB := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previousDB })

		state := bson.D{
			{Key: "_id", Value: "static"},
			{Key: "initialized", Value: true},
			{Key: "sources", Value: bson.D{{Key: "one", Value: bson.D{
				{Key: "dataset", Value: "static-dataset"},
				{Key: "generation", Value: "generation-1"},
			}}}},
		}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".stop_service_state", mtest.FirstBatch, state),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".stop_service_refs", mtest.FirstBatch,
				bson.D{{Key: "stopref", Value: "stop-a"}, {Key: "serviceref", Value: "service-primary"}},
				bson.D{{Key: "stopref", Value: "stop-a-alias"}, {Key: "serviceref", Value: "service-alias"}},
				bson.D{{Key: "stopref", Value: "stop-a-platform"}, {Key: "serviceref", Value: "shared-ref"}},
				bson.D{{Key: "stopref", Value: "stop-b"}, {Key: "serviceref", Value: "shared-ref"}},
				bson.D{{Key: "stopref", Value: "stop-b-alias"}, {Key: "serviceref", Value: "shared-ref"}},
			),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".services", mtest.FirstBatch,
				bson.D{{Key: "primaryidentifier", Value: "service-primary"}, {Key: "servicename", Value: "Primary"}},
				bson.D{{Key: "primaryidentifier", Value: "service-canonical-alias"}, {Key: "otheridentifiers", Value: bson.A{"service-alias"}}, {Key: "servicename", Value: "Alias"}},
				bson.D{{Key: "primaryidentifier", Value: "canonical-a"}, {Key: "otheridentifiers", Value: bson.A{"shared-ref"}}, {Key: "servicename", Value: "A"}},
				bson.D{{Key: "primaryidentifier", Value: "canonical-b"}, {Key: "otheridentifiers", Value: bson.A{"shared-ref"}}, {Key: "servicename", Value: "B"}},
			),
		)

		stops := []*ctdf.Stop{
			{PrimaryIdentifier: "stop-a", OtherIdentifiers: []string{"stop-a-alias"}, Platforms: []*ctdf.StopPlatform{{PrimaryIdentifier: "stop-a-platform"}}},
			{PrimaryIdentifier: "stop-b", OtherIdentifiers: []string{"stop-b-alias"}},
			{PrimaryIdentifier: "stop-empty"},
		}
		got, err := (Source{}).ServicesByStopsQuery(query.ServicesByStops{Stops: stops})
		if err != nil {
			mt.Fatal(err)
		}
		want := map[string][]string{
			"stop-a":     {"canonical-a", "canonical-b", "service-canonical-alias", "service-primary"},
			"stop-b":     {"canonical-a", "canonical-b"},
			"stop-empty": {},
		}
		gotIDs := make(map[string][]string, len(got))
		for stopID, services := range got {
			gotIDs[stopID] = []string{}
			for _, service := range services {
				gotIDs[stopID] = append(gotIDs[stopID], service.PrimaryIdentifier)
				if service.OtherIdentifiers != nil {
					mt.Fatalf("returned service %s exposes internal aliases: %v", service.PrimaryIdentifier, service.OtherIdentifiers)
				}
			}
		}
		if !reflect.DeepEqual(gotIDs, want) {
			mt.Fatalf("service IDs = %#v, want %#v", gotIDs, want)
		}
		if got["stop-empty"] == nil {
			mt.Fatal("empty stop services should be a present empty slice")
		}

		commands := map[string]int{}
		var queriedStopRefs []string
		for _, event := range mt.GetAllStartedEvents() {
			collection := event.Command.Lookup("find").StringValue()
			commands[collection]++
			if collection == "stop_service_refs" {
				values, err := event.Command.Lookup("filter", "stopref", "$in").Array().Values()
				if err != nil {
					mt.Fatalf("read raw stop refs filter: %v", err)
				}
				for _, value := range values {
					queriedStopRefs = append(queriedStopRefs, value.StringValue())
				}
			}
		}
		if commands["stop_service_refs"] != 1 || commands["services"] != 1 || commands["journeys"] != 0 {
			mt.Fatalf("collection reads = %#v, want one relation query, one service query, and no journey scan", commands)
		}
		sort.Strings(queriedStopRefs)
		wantStopRefs := []string{"stop-a", "stop-a-alias", "stop-a-platform", "stop-b", "stop-b-alias", "stop-empty"}
		if !reflect.DeepEqual(queriedStopRefs, wantStopRefs) {
			mt.Fatalf("queried raw stop refs = %v, want %v", queriedStopRefs, wantStopRefs)
		}
	})
}
