package events

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/alicebob/miniredis/v2"
	"github.com/expr-lang/expr"
	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"github.com/travigo/travigo/pkg/redis_client"
)

func TestPublishedJourneySnapshotsPreserveNotifications(t *testing.T) {
	useNotificationTestDataSource(t)
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	connection := rmq.NewTestConnection()
	oldClient, oldConnection := redis_client.Client, redis_client.QueueConnection
	redis_client.Client, redis_client.QueueConnection = client, connection
	t.Cleanup(func() {
		redis_client.Client, redis_client.QueueConnection = oldClient, oldConnection
		_ = client.Close()
	})

	day := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	clock := time.Date(0, 1, 1, 12, 0, 0, 0, time.UTC)
	journey := &ctdf.RealtimeJourney{
		PrimaryIdentifier: "realtime", JourneyRunDate: day,
		Journey: &ctdf.Journey{
			PrimaryIdentifier: "schedule", DestinationDisplay: "London", DepartureTime: clock,
			Path: []*ctdf.JourneyPathItem{{
				OriginStopRef: "origin", DestinationStopRef: "dest",
				OriginDepartureTime: clock, DestinationArrivalTime: clock.Add(10 * time.Minute),
				Track:           []ctdf.Location{{Type: "Point", Coordinates: []float64{0, 51}}},
				DestinationStop: &ctdf.Stop{PrimaryName: "Full stop document"},
			}},
		},
		Stops: map[string]*ctdf.RealtimeJourneyStops{
			"dest@1": {StopRef: "dest", JourneyStopIndex: 1, ArrivalTime: day.Add(12*time.Hour + 10*time.Minute)},
		},
	}
	ctx := context.Background()
	if err := realtimestore.SaveRealtimeJourney(ctx, journey); err != nil {
		t.Fatal(err)
	}
	journey.ActivelyTracked, journey.Cancelled, journey.SuppressFromDepartures = true, true, true
	journey.ReplacedByJourneyRef = "replacement"
	journey.VehicleLocationDescription = "Approaching Cambridge"
	journey.NextStopRef, journey.NextStopIndex = "dest", 1
	journey.NextStop = &ctdf.Stop{PrimaryName: "Cambridge"}
	journey.Stops["dest@1"].Platform = "2"
	journey.Stops["dest@1"].ArrivalTime = day.Add(12*time.Hour + 15*time.Minute)
	if err := realtimestore.SaveRealtimeJourney(ctx, journey); err != nil {
		t.Fatal(err)
	}
	journey.Stops["dest@1"].Platform = "3"
	if err := realtimestore.SaveRealtimeJourney(ctx, journey); err != nil {
		t.Fatal(err)
	}
	if len(journey.Journey.Path[0].Track) == 0 || journey.Journey.Path[0].DestinationStop == nil {
		t.Fatal("event compaction changed the live journey")
	}

	fullJSON, err := json.Marshal(journey)
	if err != nil {
		t.Fatal(err)
	}
	var fullBody map[string]interface{}
	if err := json.Unmarshal(fullJSON, &fullBody); err != nil {
		t.Fatal(err)
	}
	notifications := rmq.NewTestQueue("notify")
	consumer := &EventsBatchConsumer{NotifyQueue: notifications, Subscriptions: newEventSubscriptionCache()}
	seen := map[ctdf.EventType]bool{}
	for _, payload := range connection.GetDeliveries("events-queue") {
		var compact ctdf.Event
		if err := json.Unmarshal([]byte(payload), &compact); err != nil {
			t.Fatal(err)
		}
		seen[compact.Type] = true
		subscription := ctdf.UserNotificationSubscription{
			EventType: compact.Type, UserID: "user",
			Values: ctdf.UserNotificationSubscriptionValues{JourneyRef: "schedule", PlatformStopRefs: []string{"dest"}},
		}
		if err := subscription.Compile(); err != nil {
			t.Fatal(err)
		}
		matches, err := expr.Run(subscription.Program, compact)
		if err != nil || matches != true {
			t.Fatalf("compact %s no longer matches: %v (%v)", compact.Type, matches, err)
		}
		full := compact
		if nested, ok := compact.Body.(map[string]interface{})["RealtimeJourney"]; ok && nested != nil {
			body := map[string]interface{}{}
			for key, value := range compact.Body.(map[string]interface{}) {
				body[key] = value
			}
			body["RealtimeJourney"] = fullBody
			full.Body = body
		} else {
			full.Body = fullBody
		}
		if got, want := GetNotificationData(&compact, subscription), GetNotificationData(&full, subscription); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s notification changed: got %+v, want %+v", compact.Type, got, want)
		}
		if got, want := notificationTargetURL(&compact, subscription), notificationTargetURL(&full, subscription); got != want {
			t.Fatalf("%s link changed: %s != %s", compact.Type, got, want)
		}
		consumer.Subscriptions.byEventType[compact.Type] = []ctdf.UserNotificationSubscription{subscription}
		fullPayload, err := json.Marshal(full)
		if err != nil {
			t.Fatal(err)
		}
		compactDelivery, legacyDelivery := rmq.NewTestDeliveryString(payload), rmq.NewTestDeliveryString(string(fullPayload))
		consumer.Consume(rmq.Deliveries{compactDelivery, legacyDelivery})
		if compactDelivery.State != rmq.Acked || legacyDelivery.State != rmq.Acked {
			t.Fatal("compact or legacy event was not acknowledged")
		}
	}
	if len(seen) != 9 || len(notifications.LastDeliveries) != 18 {
		t.Fatalf("covered %d event types and sent %d notifications, want 9 and 18", len(seen), len(notifications.LastDeliveries))
	}

	unsubscribed := rmq.NewTestDeliveryString(`{"Type":"unused","Body":{"large":"ignored"}}`)
	consumer.Consume(rmq.Deliveries{unsubscribed})
	if unsubscribed.State != rmq.Acked || len(notifications.LastDeliveries) != 18 {
		t.Fatal("unsubscribed event was not discarded and acknowledged")
	}
}
