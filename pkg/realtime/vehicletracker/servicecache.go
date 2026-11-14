package vehicletracker

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/sync/singleflight"
)

// Bound both retention and staleness. Failed or missing lookups are never cached.
const trackedServiceCacheTTL = 5 * time.Minute
const trackedServiceCacheMaxEntries = 1024

type trackedServiceEntry struct {
	service *ctdf.Service
	expires time.Time
}
type trackedServiceCache struct {
	mu      sync.Mutex
	entries map[string]trackedServiceEntry
	loads   singleflight.Group
}

var processTrackedServiceCache trackedServiceCache

func cachedTrackedService(ref string) (*ctdf.Service, error) {
	return processTrackedServiceCache.get(ref, func() (*ctdf.Service, error) {
		var service ctdf.Service
		err := database.GetCollection("services").FindOne(context.Background(), bson.M{"$or": bson.A{bson.M{"primaryidentifier": ref}, bson.M{"otheridentifiers": ref}}}).Decode(&service)
		return &service, err
	})
}

func (cache *trackedServiceCache) get(ref string, load func() (*ctdf.Service, error)) (*ctdf.Service, error) {
	if ref == "" {
		return nil, nil
	}
	lookup := func() *ctdf.Service {
		cache.mu.Lock()
		defer cache.mu.Unlock()
		entry, ok := cache.entries[ref]
		if ok && time.Now().Before(entry.expires) {
			return entry.service
		}
		if ok {
			delete(cache.entries, ref)
		}
		return nil
	}
	if service := lookup(); service != nil {
		return service, nil
	}
	result, err, _ := cache.loads.Do(ref, func() (interface{}, error) {
		if service := lookup(); service != nil {
			return service, nil
		}
		service, err := load()
		if err != nil || service == nil {
			return nil, err
		}
		cache.mu.Lock()
		defer cache.mu.Unlock()
		if cache.entries == nil {
			cache.entries = make(map[string]trackedServiceEntry)
		}
		if len(cache.entries) >= trackedServiceCacheMaxEntries {
			for key := range cache.entries {
				delete(cache.entries, key)
				break
			}
		}
		cache.entries[ref] = trackedServiceEntry{service, time.Now().Add(trackedServiceCacheTTL)}
		return service, nil
	})
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if service, ok := result.(*ctdf.Service); ok {
		return service, nil
	}
	return nil, nil
}
