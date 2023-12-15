package cachedresults

import (
	"bytes"
	"context"
	"encoding/gob"
	"time"

	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
	redisstore "github.com/eko/gocache/store/redis/v4"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/redis_client"
)

type Cache struct {
	Cache *cache.Cache[[]byte]
}

func (c *Cache) Setup() {
	redisStore := redisstore.NewRedis(redis_client.Client, store.WithExpiration(12*time.Hour))

	c.Cache = cache.New[[]byte](redisStore)
}

func Set(c *Cache, key string, object any, expiration time.Duration) {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(object); err != nil {
		log.Error().Err(err).Msg("marshall")
	}
	marshalledObject := buf.Bytes()

	c.Cache.Set(context.Background(), key, marshalledObject, store.WithExpiration(expiration))
}

func Get[T any](c *Cache, key string) (T, error) {
	cachedObjectBytes, err := c.Cache.Get(context.Background(), key)
	var cachedObject T

	reader := bytes.NewReader(cachedObjectBytes)
	dec := gob.NewDecoder(reader)
	if err := dec.Decode(&cachedObject); err != nil {
		return cachedObject, err
	}

	return cachedObject, err
}
