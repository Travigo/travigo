package vehicletracker

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/telemetry"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestServiceAlertsOnlyIdentifyJourneysForGTFSRefsWithTrips(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, tc := range []struct {
		source       string
		references   []map[string]string
		collections  []string
		queryIDs     []string
		identifiers  []string
		journeyCalls int64
	}{
		{
			source: "GTFS-RT",
			references: []map[string]string{
				{"StopID": "stop", "LinkedDataset": "schedule"},
				{"RouteID": "route", "LinkedDataset": "schedule"},
				{"TripID": "trip", "LinkedDataset": "schedule"},
				{"TripID": "trip-2", "LinkedDataset": "schedule"},
			},
			collections:  []string{"journeys", "stops", "services"},
			queryIDs:     []string{"", "matched-stop", "matched-service"},
			identifiers:  []string{"matched-stop", "matched-service", "matched-journey", "matched-journey-2"},
			journeyCalls: 2,
		},
		{
			source: "siri-sx",
			references: []map[string]string{
				{"StopPointRef": "stop", "LinkedDataset": "schedule"},
				{"LineRef": "route", "OperatorRef": "operator", "LinkedDataset": "schedule"},
			},
			collections: []string{"stops", "services"},
			queryIDs:    []string{"matched-stop", "matched-service"},
			identifiers: []string{"matched-stop", "matched-service"},
		},
	} {
		mt.Run(tc.source, func(mt *mtest.T) {
			setupIdentificationTestRedis(mt.T)
			previousDatabase := database.Instance
			database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
			mt.Cleanup(func() { database.Instance = previousDatabase })
			for i, collection := range tc.collections {
				namespace := mt.DB.Name() + "." + collection
				if collection == "journeys" {
					mt.AddMockResponses(mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch,
						bson.D{{Key: "primaryidentifier", Value: "matched-journey"}, {Key: "otheridentifiers", Value: bson.M{"GTFS-TripID": "trip"}}},
						bson.D{{Key: "primaryidentifier", Value: "matched-journey-2"}, {Key: "otheridentifiers", Value: bson.M{"GTFS-TripID": "trip-2"}}}))
				} else {
					mt.AddMockResponses(mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch,
						bson.D{{Key: "primaryidentifier", Value: tc.queryIDs[i]}}))
				}
			}
			mt.AddMockResponses(mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}))

			now := time.Now()
			payload, err := json.Marshal(VehicleUpdateEvent{
				LocalID: "alert", SourceType: tc.source, MessageType: VehicleUpdateEventTypeServiceAlert,
				SubmittedAt: now, RecordedAt: now,
				DataSource:         &ctdf.DataSourceReference{DatasetID: "realtime"},
				ServiceAlertUpdate: &ServiceAlertUpdate{IdentifyingInformation: tc.references},
			})
			if err != nil {
				mt.Fatal(err)
			}
			before := telemetry.Snapshot()
			delivery := rmq.NewTestDeliveryString(string(payload))
			(&BatchConsumer{}).Consume(rmq.Deliveries{delivery})
			after := telemetry.Snapshot()
			if delivery.State != rmq.Acked {
				mt.Fatalf("expected alert to be acknowledged, got %v", delivery.State)
			}
			for name, want := range map[string]int64{
				"service_alerts": 1, "service_alert_references": int64(len(tc.references)),
				"service_alert_journey_lookups": tc.journeyCalls,
			} {
				if got := after.Counters[name] - before.Counters[name]; got != want {
					mt.Errorf("%s = %d, want %d", name, got, want)
				}
			}
			wantQueries := int64(0)
			if tc.journeyCalls > 0 {
				wantQueries = 1
			}
			if got := after.Stages["alert_journey_query"].Count - before.Stages["alert_journey_query"].Count; got != wantQueries {
				mt.Errorf("alert trip resolver ran %d queries, want %d", got, wantQueries)
			}
			events := mt.GetAllStartedEvents()
			if len(events) != len(tc.collections)+1 || events[len(events)-1].CommandName != "update" {
				mt.Fatalf("expected one query per applicable reference and one alert update, got %d commands", len(events))
			}
			fields := events[len(events)-1].Command.Lookup("updates", "0", "u", "$set", "matchedidentifiers").Array()
			values, err := fields.Values()
			if err != nil {
				mt.Fatal(err)
			}
			matched := make([]string, 0, len(values))
			for _, value := range values {
				matched = append(matched, value.StringValue())
			}
			if !slices.Equal(matched, tc.identifiers) {
				mt.Errorf("matched identifiers = %v, want %v", matched, tc.identifiers)
			}
		})
	}
}
