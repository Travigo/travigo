package dvlaves

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestRegistrationFromBODSVehicleRef(t *testing.T) {
	for ref, want := range map[string]string{
		" aa19 aaa ": "AA19AAA",
		"A123 ABC":   "A123ABC",
		"ABC 123D":   "ABC123D",
		"10011":      "",
		"OP:1242":    "",
	} {
		if got := RegistrationFromVehicleRef(ref); got != want {
			t.Errorf("registration from %q = %q, want %q", ref, got, want)
		}
	}
}

func TestVehicleLookupCachesResponse(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, status := range []int{http.StatusOK, http.StatusNotFound, http.StatusBadRequest, http.StatusForbidden, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		mt.Run(http.StatusText(status), func(mt *mtest.T) {
			redisServer := miniredis.RunT(mt.T)
			redisClient := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
			defer redisClient.Close()

			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct{ RegistrationNumber string }
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
					r.Method != http.MethodPost || r.URL.Path != "/vehicle-enquiry/v1/vehicles" ||
					r.Header.Get("x-api-key") != "test-key" || r.Header.Get("Content-Type") != "application/json" ||
					body.RegistrationNumber != "AA19AAA" {
					mt.Error("unexpected DVLA VES request")
				}
				responseStatus := status
				if requests.Add(1) > 1 {
					responseStatus = http.StatusOK
				}
				w.WriteHeader(responseStatus)
				if responseStatus == http.StatusOK {
					_, _ = w.Write([]byte(`{"registrationNumber":"AA19AAA","make":"VOLVO","colour":"Blue","fuelType":"ELECTRICITY"}`))
				}
			}))
			defer server.Close()

			ctx := context.Background()
			client := New("test-key", mt.Coll, redisClient)
			client.endpoint = server.URL + "/vehicle-enquiry/v1/vehicles"
			namespace := mt.DB.Name() + "." + mt.Coll.Name()
			mt.AddMockResponses(
				mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch),
				mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}),
				mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}),
			)

			details, err := client.GetVehicleDataBus(ctx, " aa19 aaa ")
			cacheable := status == http.StatusOK || status == http.StatusNotFound
			if (err == nil) != cacheable {
				mt.Fatalf("unexpected lookup error %v", err)
			}
			if status == http.StatusOK && (details == nil || details.VehicleMake != "VOLVO" ||
				details.VehicleColour != "Blue" || details.PowerType != "ELECTRICITY" || details.RegistrationNumber != "AA19AAA") {
				mt.Fatalf("unexpected vehicle details %+v", details)
			}
			if status != http.StatusOK && details != nil {
				mt.Fatalf("unexpected vehicle details %+v", details)
			}

			commands := mt.GetAllStartedEvents()
			if len(commands) != 3 || commands[0].CommandName != "find" || commands[1].CommandName != "update" {
				mt.Fatalf("expected Mongo find, claim and finalization, got %v", commands)
			}
			if !cacheable {
				if commands[2].CommandName != "delete" {
					mt.Fatalf("failed lookup should delete its claim, got %s", commands[2].CommandName)
				}
				claim := commands[1].Command.Lookup("updates").Array().Index(0).Value().Document().Lookup("u", "$set", "claim").ObjectID()
				filter := commands[2].Command.Lookup("deletes").Array().Index(0).Value().Document().Lookup("q").Document()
				if filter.Lookup("_id").StringValue() != "AA19AAA" || filter.Lookup("claim").ObjectID() != claim {
					mt.Fatalf("claim deletion was not scoped to the failed request: %v", filter)
				}
				cooldown := 550 * time.Millisecond
				switch status {
				case http.StatusTooManyRequests:
					cooldown = time.Minute
				case http.StatusForbidden:
					cooldown = 5 * time.Minute
				}
				if ttl := redisServer.TTL(rateKey); ttl != cooldown {
					mt.Fatalf("unexpected Redis rate limit TTL %v, want %v", ttl, cooldown)
				}
				redisServer.FastForward(cooldown)
				mt.AddMockResponses(
					mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch),
					mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}),
					mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}),
				)
				got, err := client.GetVehicleDataBus(ctx, "AA19AAA")
				if err != nil || got == nil || got.VehicleMake != "VOLVO" || requests.Load() != 2 {
					mt.Fatalf("later lookup did not retry DVLA successfully: %+v, %v", got, err)
				}
				return
			}

			if commands[2].CommandName != "update" {
				mt.Fatalf("expected a cache write, got %s", commands[2].CommandName)
			}
			write := commands[2].Command.Lookup("updates").Array().Index(0).Value().Document()
			expires := write.Lookup("u", "$set", "expiresat").Time()
			wantTTL := successTTL
			if status == http.StatusNotFound {
				wantTTL = negativeTTL
			}
			if diff := time.Until(expires) - wantTTL; diff < -time.Second || diff > 0 {
				mt.Fatalf("unexpected cache TTL %v", time.Until(expires))
			}

			// Another provider instance reuses the result without contacting DVLA.
			mt.AddMockResponses(mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch, bson.D{
				{Key: "_id", Value: "AA19AAA"}, {Key: "expiresat", Value: expires}, {Key: "details", Value: details},
			}))
			fromCache := New("test-key", mt.Coll, nil)
			got, err := fromCache.GetVehicleDataBus(ctx, "AA19AAA")
			if err != nil || (got == nil) != (details == nil) || (got != nil && *got != *details) || requests.Load() != 1 {
				mt.Fatalf("unexpected cached vehicle %+v, %v", got, err)
			}
		})
	}
}

func TestVehicleLookupReusesCacheAfterConcurrentClaim(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("another replica claimed the miss", func(mt *mtest.T) {
		namespace := mt.DB.Name() + "." + mt.Coll.Name()
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch),
			mtest.CreateWriteErrorsResponse(mtest.WriteError{Code: 11000, Message: "duplicate key"}),
			mtest.CreateCursorResponse(0, namespace, mtest.FirstBatch, bson.D{
				{Key: "_id", Value: "AA19AAA"},
				{Key: "expiresat", Value: time.Now().Add(successTTL)},
				{Key: "details", Value: bson.M{"registrationnumber": "AA19AAA", "vehiclemake": "VOLVO"}},
			}),
		)
		client := New("test-key", mt.Coll, nil)
		got, err := client.GetVehicleDataBus(context.Background(), "AA19AAA")
		if err != nil || got == nil || got.VehicleMake != "VOLVO" {
			mt.Fatalf("expected result from the winning replica, got %+v, %v", got, err)
		}
	})
}
