package railutils

import (
	"context"
	"encoding/json"
	"time"

	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
	redisstore "github.com/eko/gocache/store/redis/v4"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/redis_client"
	"go.mongodb.org/mongo-driver/bson"
)

type StopCache struct {
	Cache *cache.Cache[string]
}

func (s *StopCache) Setup() {
	redisStore := redisstore.NewRedis(redis_client.Client, store.WithExpiration(90*time.Minute))

	s.Cache = cache.New[string](redisStore)
}

func (s *StopCache) Get(identifier string) *ctdf.Stop {
	// Performance Review [Set J] [PR119] [P2] (realtime): Even a warm hit costs a Redis round trip
	// and full Stop JSON decode for every Darwin/NROD location. Resolve unique
	// aliases in batches and add a bounded immutable process cache for the identity,
	// aliases and location actually needed. Coalesce misses and invalidate/version
	// with stop imports; measure remote reads per frame as well as the hit rate.
	var stop *ctdf.Stop

	stopCacheValue, err := s.Cache.Get(context.Background(), identifier)
	if err == nil {
		if stopCacheValue == "N/A" {
			return nil
		}

		json.Unmarshal([]byte(stopCacheValue), &stop)
		return stop
	}

	stopCollection := database.GetCollection("stops")
	stopCollection.FindOne(context.Background(), bson.M{"otheridentifiers": identifier}).Decode(&stop)

	// Performance Review [Set J] [PR120] [P1] (realtime): The ignored Decode error turns a transient
	// MongoDB failure into "N/A" for the same 90-minute TTL as a valid stop. Updates
	// then skip that location after the database has recovered. Negative-cache only
	// confirmed absence, use a shorter bounded expiry, and propagate transient
	// failures into the consumer's retry/backpressure path instead of acknowledging.
	if stop == nil {
		s.Cache.Set(context.Background(), identifier, "N/A")
	} else {
		stopJSON, _ := json.Marshal(stop)
		s.Cache.Set(context.Background(), identifier, string(stopJSON))
	}

	return stop
}
