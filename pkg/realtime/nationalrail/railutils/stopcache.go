package railutils

import (
	"context"
	"encoding/json"
	"fmt"
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

func (s *StopCache) Get(identifierType string, identifier string) *ctdf.Stop {
	var stop *ctdf.Stop
	fullStopCacheID := fmt.Sprintf("GB:%s:%s", identifierType, identifier)

	stopCacheValue, err := s.Cache.Get(context.Background(), fullStopCacheID)
	if err == nil {
		if stopCacheValue == "N/A" {
			return nil
		}

		json.Unmarshal([]byte(stopCacheValue), &stop)
		return stop
	}

	stopCollection := database.GetCollection("stops")
	stopCollection.FindOne(context.Background(), bson.M{fmt.Sprintf("otheridentifiers.%s", identifierType): identifier}).Decode(&stop)

	if stop == nil {
		s.Cache.Set(context.Background(), fullStopCacheID, "N/A")
	} else {
		stopJSON, _ := json.Marshal(stop)
		s.Cache.Set(context.Background(), fullStopCacheID, string(stopJSON))
	}

	return stop
}
