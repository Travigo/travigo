package nrod

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/redis_client"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestVSTPCancellationStoresDayInstanceAlerts(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	previousRedis := redis_client.Client
	redis_client.Client = client
	t.Cleanup(func() { redis_client.Client = previousRedis; client.Close() })

	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("matching days", func(mt *mtest.T) {
		previousDB := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previousDB })

		start := time.Now().UTC().Truncate(24 * time.Hour)
		end := start.AddDate(0, 0, 2)
		firstRun, secondRun := start.AddDate(0, 0, 1), end
		days := []byte("0000000")
		days[convertWeekday(firstRun)] = '1'
		days[convertWeekday(secondRun)] = '1'
		journey := ctdf.Journey{PrimaryIdentifier: "train", Availability: &ctdf.Availability{Match: []ctdf.AvailabilityRule{{Type: ctdf.AvailabilityMatchAll}}}}
		encoded, err := bson.Marshal(journey)
		if err != nil {
			mt.Fatal(err)
		}
		var document bson.D
		if err := bson.Unmarshal(encoded, &document); err != nil {
			mt.Fatal(err)
		}
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".journeys", mtest.FirstBatch, document),
			mtest.CreateSuccessResponse(),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".services", mtest.FirstBatch),
		)
		var message VSTPMessage
		message.VSTP.Schedule.TrainUID = "uid"
		message.VSTP.Schedule.StartDate = start.Format(ctdf.YearMonthDayFormat)
		message.VSTP.Schedule.EndDate = end.Format(ctdf.YearMonthDayFormat)
		message.VSTP.Schedule.DayRuns = string(days)
		message.processCancellation()

		var alert ctdf.ServiceAlert
		for _, event := range mt.GetAllStartedEvents() {
			if event.CommandName != "update" || event.Command.Lookup("update").StringValue() != "service_alerts" {
				continue
			}
			var command struct {
				Updates []struct {
					Update struct {
						Set ctdf.ServiceAlert `bson:"$set"`
					} `bson:"u"`
				} `bson:"updates"`
			}
			if err := bson.Unmarshal(event.Command, &command); err != nil {
				mt.Fatal(err)
			}
			if len(command.Updates) != 1 {
				mt.Fatal("expected one service-alert update")
			}
			alert = command.Updates[0].Update.Set
		}
		if alert.AlertType != ctdf.ServiceAlertTypeJourneyCancelled || len(alert.MatchedIdentifiers) != 2 || alert.MatchedIdentifiers[0] != fmt.Sprintf("DAYINSTANCEOF:%s:train", firstRun.Format(ctdf.YearMonthDayFormat)) || alert.MatchedIdentifiers[1] != fmt.Sprintf("DAYINSTANCEOF:%s:train", secondRun.Format(ctdf.YearMonthDayFormat)) || !alert.IsValid(time.Now()) {
			mt.Fatalf("unexpected cancellation alert: %#v", alert)
		}
		id := fmt.Sprintf("gb-nationalrail-vstp-cancellation-%s:%s:train", message.VSTP.Schedule.StartDate, message.VSTP.Schedule.EndDate)
		stored, err := client.Get(context.Background(), "realtime-journey:details/"+id).Bytes()
		if err != nil {
			mt.Fatal(err)
		}
		var realtime struct {
			RunDate   time.Time `json:"jrd"`
			Cancelled bool      `json:"can"`
		}
		if err := json.Unmarshal(stored, &realtime); err != nil {
			mt.Fatal(err)
		}
		if !realtime.Cancelled || realtime.RunDate.Format(ctdf.YearMonthDayFormat) != firstRun.Format(ctdf.YearMonthDayFormat) {
			mt.Fatalf("unexpected realtime run: %#v", realtime)
		}
	})
}

func TestJourneyRunsDuringVSTPCancellationHonoursDaysRun(t *testing.T) {
	monday := time.Date(2026, 7, 6, 0, 0, 0, 0, time.UTC)
	journey := &ctdf.Journey{Availability: &ctdf.Availability{
		Match: []ctdf.AvailabilityRule{{Type: ctdf.AvailabilityMatchAll}},
	}}

	if len(vstpMatchingServiceDates(journey, monday, monday.AddDate(0, 0, 1), "1000000")) != 1 {
		t.Fatal("Monday-running VSTP cancellation should match the permanent journey")
	}
	if len(vstpMatchingServiceDates(journey, monday, monday, "0000000")) != 0 {
		t.Fatal("VSTP cancellation with no operating days should not match")
	}
	if got := vstpMatchingServiceDates(journey, monday, monday.AddDate(0, 0, 1), "1000000"); len(got) != 1 || got[0] != "2026-07-06" {
		t.Fatalf("matching VSTP dates = %#v, want only 2026-07-06", got)
	}
}

func TestVSTPRealtimeTimeoutMinutesUsesVSTPExpiryWindow(t *testing.T) {
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	endDate := time.Date(2026, 7, 11, 0, 0, 0, 0, time.UTC)

	if got, want := vstpRealtimeTimeoutMinutes(endDate, now), 3601; got != want {
		t.Fatalf("timeout = %d minutes, want %d", got, want)
	}
	if got := vstpRealtimeTimeoutMinutes(now.AddDate(0, 0, -3), now); got != 0 {
		t.Fatalf("expired VSTP cancellation timeout = %d, want 0", got)
	}
}
