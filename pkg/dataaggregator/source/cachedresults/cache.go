package cachedresults

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/eko/gocache/lib/v4/cache"
	"github.com/eko/gocache/lib/v4/store"
	redisstore "github.com/eko/gocache/store/redis/v4"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/redis_client"
)

type Cache struct {
	Cache *cache.Cache[string]
}

func (c *Cache) Setup() {
	redisStore := redisstore.NewRedis(redis_client.Client, store.WithExpiration(12*time.Hour))

	c.Cache = cache.New[string](redisStore)
}

func Set(c *Cache, key string, object any, expiration time.Duration) {
	marshalledObject, _ := json.Marshal(object)

	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	if _, err := gz.Write(marshalledObject); err != nil {
		log.Error().Err(err).Msg("Failed to write gzip")
	}
	if err := gz.Close(); err != nil {
		log.Error().Err(err).Msg("Failed to close gzip")
	}

	c.Cache.Set(context.Background(), key, string(b.Bytes()), store.WithExpiration(expiration))
}

func Get[T any](c *Cache, key string) (T, error) {
	currentTime := time.Now()
	cachedObjecString, err := c.Cache.Get(context.Background(), key)
	log.Debug().Str("Length", time.Now().Sub(currentTime).String()).Msg("Cache - read")
	var cachedObject T

	if err != nil {
		return cachedObject, err
	}

	currentTime = time.Now()

	compressedBytes := bytes.NewReader([]byte(cachedObjecString))
	gzip, err := gzip.NewReader(compressedBytes)
	if err != nil {
		return cachedObject, err
	}
	uncompressedBytes, err := io.ReadAll(gzip)
	if err != nil {
		return cachedObject, err
	}

	log.Debug().Str("Length", time.Now().Sub(currentTime).String()).Msg("Cache - dezip")

	currentTime = time.Now()
	err = json.Unmarshal(uncompressedBytes, &cachedObject)
	log.Debug().Str("Length", time.Now().Sub(currentTime).String()).Msg("Cache - unmarshall")

	return cachedObject, err
}

func DeletePrefix(key string) {
	ctx := context.Background()
	iter := redis_client.Client.Scan(ctx, 0, key, 0).Iterator()
	for iter.Next(ctx) {
		redis_client.Client.Del(ctx, iter.Val()).Err()
	}
}
