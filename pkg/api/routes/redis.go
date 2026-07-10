package routes

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/redis_client"
)

const (
	defaultRedisMemorySample  = 250
	maxRedisMemorySample      = 10000000
	maxRedisMemoryPerCategory = 50
	redisMemoryScanCount      = 500
	redisMemoryTimeout        = 5 * time.Second
)

type redisMemorySnapshot struct {
	CapturedAt      time.Time             `json:"captured_at"`
	ScanComplete    bool                  `json:"scan_complete"`
	ScannedKeys     int64                 `json:"scanned_keys"`
	SampleLimit     int                   `json:"sample_limit"`
	SampledKeys     int                   `json:"sampled_keys"`
	MemoryInfo      redisMemoryInfo       `json:"memory_info"`
	MemoryInfoError string                `json:"memory_info_error,omitempty"`
	Categories      []redisMemoryCategory `json:"categories"`
}

type redisMemoryInfo struct {
	UsedMemoryBytes    int64   `json:"used_memory_bytes"`
	UsedMemoryRSSBytes int64   `json:"used_memory_rss_bytes"`
	PeakMemoryBytes    int64   `json:"peak_memory_bytes"`
	MaxMemoryBytes     int64   `json:"maxmemory_bytes"`
	FragmentationRatio float64 `json:"fragmentation_ratio"`
	MaxMemoryPolicy    string  `json:"maxmemory_policy,omitempty"`
}

type redisMemoryCategory struct {
	Category                string `json:"category"`
	KeyCount                int64  `json:"key_count"`
	SampledKeys             int64  `json:"sampled_keys"`
	SampledMemoryBytes      int64  `json:"sampled_memory_bytes"`
	EstimatedMemoryBytes    int64  `json:"estimated_memory_bytes"`
	MemoryEstimateAvailable bool   `json:"memory_estimate_available"`
}

type redisMemoryCategoryAccumulator struct {
	keyCount     int64
	sampledKeys  []string
	sampledBytes int64
	measuredKeys int64
}

type redisMemorySample struct {
	category string
	key      string
	usage    *redis.IntCmd
}

func RedisRouter(router fiber.Router) {
	router.Get("/memory", redisMemory)
}

func redisMemory(c *fiber.Ctx) error {
	sampleLimit := defaultRedisMemorySample
	if rawSample := c.Query("sample"); rawSample != "" {
		parsed, err := strconv.Atoi(rawSample)
		if err != nil || parsed < 1 || parsed > maxRedisMemorySample {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
				"error": "sample must be an integer between 1 and 10000000",
			})
		}
		sampleLimit = parsed
	}

	ctx, cancel := context.WithTimeout(context.Background(), redisMemoryTimeout)
	defer cancel()

	snapshot, err := snapshotRedisMemory(ctx, sampleLimit)
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	c.Set(fiber.HeaderCacheControl, "no-store")
	return c.JSON(snapshot)
}

func snapshotRedisMemory(ctx context.Context, sampleLimit int) (redisMemorySnapshot, error) {
	snapshot := redisMemorySnapshot{
		CapturedAt:  time.Now().UTC(),
		SampleLimit: sampleLimit,
		Categories:  []redisMemoryCategory{},
	}

	info, err := redis_client.Client.Info(ctx, "memory").Result()
	if err != nil {
		snapshot.MemoryInfoError = err.Error()
	} else {
		snapshot.MemoryInfo = parseRedisMemoryInfo(info)
	}

	categories := map[string]*redisMemoryCategoryAccumulator{}
	var cursor uint64
	for {
		keys, nextCursor, err := redis_client.Client.Scan(ctx, cursor, "*", redisMemoryScanCount).Result()
		if err != nil {
			return snapshot, err
		}

		for _, key := range keys {
			categoryName := redisKeyCategory(key)
			category := categories[categoryName]
			if category == nil {
				category = &redisMemoryCategoryAccumulator{}
				categories[categoryName] = category
			}
			category.keyCount++
			snapshot.ScannedKeys++

			// Cap samples per category so one large namespace cannot consume the
			// entire diagnostic budget.
			if len(category.sampledKeys) < maxRedisMemoryPerCategory && snapshot.SampledKeys < sampleLimit {
				category.sampledKeys = append(category.sampledKeys, key)
				snapshot.SampledKeys++
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			snapshot.ScanComplete = true
			break
		}
	}

	samples := make([]redisMemorySample, 0, snapshot.SampledKeys)
	for categoryName, category := range categories {
		for _, key := range category.sampledKeys {
			samples = append(samples, redisMemorySample{
				category: categoryName,
				key:      key,
			})
		}
	}
	_, _ = redis_client.Client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for index := range samples {
			samples[index].usage = pipe.MemoryUsage(ctx, samples[index].key)
		}
		return nil
	})

	for _, sample := range samples {
		if sample.usage == nil {
			continue
		}
		usage, err := sample.usage.Result()
		if err != nil {
			continue
		}
		category := categories[sample.category]
		category.sampledBytes += usage
		category.measuredKeys++
	}

	categoryNames := make([]string, 0, len(categories))
	for categoryName := range categories {
		categoryNames = append(categoryNames, categoryName)
	}
	sort.Strings(categoryNames)

	for _, categoryName := range categoryNames {
		category := categories[categoryName]
		result := redisMemoryCategory{
			Category:           categoryName,
			KeyCount:           category.keyCount,
			SampledKeys:        category.measuredKeys,
			SampledMemoryBytes: category.sampledBytes,
		}
		if category.measuredKeys > 0 {
			result.MemoryEstimateAvailable = true
			averageBytes := category.sampledBytes / category.measuredKeys
			result.EstimatedMemoryBytes = averageBytes * category.keyCount
		}
		snapshot.Categories = append(snapshot.Categories, result)
	}

	return snapshot, nil
}

func parseRedisMemoryInfo(info string) redisMemoryInfo {
	values := map[string]string{}
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if ok {
			values[key] = strings.TrimSpace(value)
		}
	}

	return redisMemoryInfo{
		UsedMemoryBytes:    parseRedisInt(values["used_memory"]),
		UsedMemoryRSSBytes: parseRedisInt(values["used_memory_rss"]),
		PeakMemoryBytes:    parseRedisInt(values["used_memory_peak"]),
		MaxMemoryBytes:     parseRedisInt(values["maxmemory"]),
		FragmentationRatio: parseRedisFloat(values["mem_fragmentation_ratio"]),
		MaxMemoryPolicy:    values["maxmemory_policy"],
	}
}

func parseRedisInt(value string) int64 {
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

func parseRedisFloat(value string) float64 {
	parsed, _ := strconv.ParseFloat(value, 64)
	return parsed
}

func redisKeyCategory(key string) string {
	switch {
	case strings.HasPrefix(key, "cachedresults/"):
		return "cache/cachedresults"
	case strings.HasPrefix(key, "realtime-journey:details/"):
		return "realtime/journey-details"
	case strings.HasPrefix(key, "realtime-journey:mapping:"):
		return "realtime/journey-mappings"
	case strings.HasPrefix(key, "realtime-journey:raildetailed:"):
		return "realtime/rail-details"
	case strings.HasPrefix(key, "realtime-journey:locationdescription/"):
		return "realtime/location-descriptions"
	case strings.HasPrefix(key, "realtime-journey:location/"):
		return "realtime/locations"
	case key == "realtime-journey:location:index":
		return "realtime/location-index"
	case strings.HasPrefix(key, "realtime-journey:tfl-indexed-stops/") || strings.HasPrefix(key, "realtime-journeys:tfl-stop:"):
		return "realtime/tfl-stop-indexes"
	case strings.HasPrefix(key, "realtime-vehicle-history:"):
		return "realtime/vehicle-history"
	case strings.HasPrefix(key, "rmq::queue::") && strings.HasSuffix(key, "::ready"):
		return "queue/ready"
	case strings.HasPrefix(key, "rmq::queue::") && strings.HasSuffix(key, "::rejected"):
		return "queue/rejected"
	case strings.HasPrefix(key, "rmq::"):
		return "queue/metadata"
	default:
		return "other"
	}
}
