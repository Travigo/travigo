package cachedresults

import (
	"time"

	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
	redisstore "github.com/eko/gocache/store/redis/v4"
	"github.com/travigo/travigo/pkg/redis_client"
)

type Cache struct {
	Cache *cache.Cache[string]
}

func (c *Cache) Setup() {
	redisStore := redisstore.NewRedis(redis_client.Client, store.WithExpiration(90*time.Minute))

	c.Cache = cache.New[string](redisStore)
}
