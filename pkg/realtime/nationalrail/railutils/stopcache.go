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

type TiplocCache struct {
	Cache *cache.Cache[string]
}

func (t *TiplocCache) Setup() {
	redisStore := redisstore.NewRedis(redis_client.Client, store.WithExpiration(90*time.Minute))

	t.Cache = cache.New[string](redisStore)
}

func (t *TiplocCache) Get(tiploc string) *ctdf.Stop {
	var stop *ctdf.Stop
	fullTiplocID := fmt.Sprintf("GB:TIPLOC:%s", tiploc)

	stopCacheValue, err := t.Cache.Get(context.Background(), fullTiplocID)
	if err == nil {
		if stopCacheValue == "N/A" {
			return nil
		}

		json.Unmarshal([]byte(stopCacheValue), &stop)
		return stop
	}

	stopCollection := database.GetCollection("stops")
	stopCollection.FindOne(context.Background(), bson.M{"otheridentifiers.Tiploc": tiploc}).Decode(&stop)

	if stop == nil {
		t.Cache.Set(context.Background(), fullTiplocID, "N/A")
	} else {
		stopJSON, _ := json.Marshal(stop)
		t.Cache.Set(context.Background(), fullTiplocID, string(stopJSON))
	}

	return stop
}
