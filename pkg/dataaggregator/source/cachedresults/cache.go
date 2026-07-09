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
	"github.com/klauspost/compress/zstd"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/redis_client"
)

type Cache struct {
	Cache *cache.Cache[string]
}

var cacheZstdEncoder, _ = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedFastest))
var cacheZstdDecoder, _ = zstd.NewReader(nil)

func (c *Cache) Setup() {
	redisStore := redisstore.NewRedis(redis_client.Client, store.WithExpiration(12*time.Hour))

	c.Cache = cache.New[string](redisStore)
}

func Set(c *Cache, key string, object any, expiration time.Duration) {
	marshalledObject, err := json.Marshal(object)
	if err != nil {
		log.Error().Err(err).Msg("Failed to marshal cached result")
		return
	}
	compressedObject := cacheZstdEncoder.EncodeAll(marshalledObject, nil)

	// TODO(low-risk): store []byte to avoid copies. The eko/gocache store
	// is typed cache.Cache[string], so the value type must be string here; switching to
	// []byte would require changing the Cache struct's generic type and all call sites
	// (a wider refactor), so the string(...) copy on Set and the []byte(...) copy on Get
	// are left as-is.
	c.Cache.Set(context.Background(), key, string(compressedObject), store.WithExpiration(expiration))
}

func Get[T any](c *Cache, key string) (T, error) {
	readStart := time.Now()
	cachedObjecString, err := c.Cache.Get(context.Background(), key)
	log.Debug().Dur("duration", time.Since(readStart)).Msg("Cache read")
	var cachedObject T

	if err != nil {
		return cachedObject, err
	}

	decompressStart := time.Now()
	decodedBytes, codec, err := decompressCachedValue([]byte(cachedObjecString))
	if err != nil {
		return cachedObject, err
	}

	decodeStart := time.Now()
	err = json.Unmarshal(decodedBytes, &cachedObject)
	log.Debug().
		Str("codec", codec).
		Dur("decompression_duration", time.Since(decompressStart)).
		Dur("json_decode_duration", time.Since(decodeStart)).
		Int("compressed_bytes", len(cachedObjecString)).
		Int("decoded_bytes", len(decodedBytes)).
		Msg("Cache decode")

	return cachedObject, err
}

func decompressCachedValue(compressed []byte) ([]byte, string, error) {
	decoded, err := cacheZstdDecoder.DecodeAll(compressed, nil)
	if err == nil {
		return decoded, "zstd", nil
	}

	gzipReader, gzipErr := gzip.NewReader(bytes.NewReader(compressed))
	if gzipErr != nil {
		return nil, "", err
	}
	defer gzipReader.Close()

	decoded, gzipErr = io.ReadAll(gzipReader)
	if gzipErr != nil {
		return nil, "", gzipErr
	}
	return decoded, "gzip", nil
}

func DeletePrefix(key string) {
	ctx := context.Background()
	iter := redis_client.Client.Scan(ctx, 0, key, 0).Iterator()
	for iter.Next(ctx) {
		redis_client.Client.Del(ctx, iter.Val()).Err()
	}
}
