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
		identifiers  []string
		journeyCalls int64
	}{
		{
			source: "GTFS-RT",
			references: []map[string]string{
				{"StopID": "stop", "LinkedDataset": "schedule"},
				{"RouteID": "route", "LinkedDataset": "schedule"},
				{"TripID": "trip", "LinkedDataset": "schedule"},
			},
			collections:  []string{"stops", "services", "journeys"},
			identifiers:  []string{"matched-stop", "matched-service", "matched-journey"},
			journeyCalls: 1,
		},
		{
			source: "siri-sx",
			references: []map[string]string{
				{"StopPointRef": "stop", "LinkedDataset": "schedule"},
				{"LineRef": "route", "OperatorRef": "operator", "LinkedDataset": "schedule"},
			},
			collections: []string{"stops", "services"},
			identifiers: []string{"matched-stop", "matched-service"},
		},
	} {
		mt.Run(tc.source, func(mt *mtest.T) {
			setupIdentificationTestRedis(mt.T)
			previousDatabase := database.Instance
			database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
			mt.Cleanup(func() { database.Instance = previousDatabase })
			for i, collection := range tc.collections {
				mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+collection, mtest.FirstBatch,
					bson.D{{Key: "primaryidentifier", Value: tc.identifiers[i]}}))
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
			if got := after.Stages["identification"].Count - before.Stages["identification"].Count; got != tc.journeyCalls {
				mt.Errorf("journey identification ran %d times, want %d", got, tc.journeyCalls)
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
