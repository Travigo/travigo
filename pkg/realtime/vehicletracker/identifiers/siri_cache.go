package identifiers

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"golang.org/x/sync/singleflight"
)

var (
	siriOperators = siriLookupCache[*ctdf.Operator]{limit: 512, ttl: 5 * time.Minute}
	siriServices  = siriLookupCache[[]string]{limit: 2048, ttl: 5 * time.Minute}
	siriJourneys  = siriLookupCache[string]{limit: 8192, ttl: 2 * time.Minute}
)

type siriCacheEntry[T any] struct {
	value T
	err   error
	until time.Time
}

type siriLookupCache[T any] struct {
	mu      sync.Mutex
	entries map[string]siriCacheEntry[T]
	loads   singleflight.Group
	limit   int
	ttl     time.Duration
}

func siriCacheKey(parts ...string) string {
	key, _ := json.Marshal(parts)
	return string(key)
}

func (c *siriLookupCache[T]) get(key string, load func() (T, error)) (T, error) {
	if entry, ok := c.lookup(key); ok {
		return entry.value, entry.err
	}
	result, err, _ := c.loads.Do(key, func() (any, error) {
		if entry, ok := c.lookup(key); ok {
			return entry, nil
		}
		value, err := load()
		if err != nil && !isSiriNoMatch(err) {
			return nil, err
		}
		ttl := c.ttl
		if err != nil {
			ttl = time.Minute
		}
		entry := siriCacheEntry[T]{value: value, err: err, until: time.Now().Add(ttl)}
		c.mu.Lock()
		if c.entries == nil {
			c.entries = make(map[string]siriCacheEntry[T])
		}
		if len(c.entries) >= c.limit {
			for oldKey := range c.entries {
				delete(c.entries, oldKey)
				break
			}
		}
		c.entries[key] = entry
		c.mu.Unlock()
		return entry, nil
	})
	if err != nil {
		var zero T
		return zero, err
	}
	entry := result.(siriCacheEntry[T])
	return entry.value, entry.err
}

func (c *siriLookupCache[T]) lookup(key string) (siriCacheEntry[T], bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if ok && !entry.until.After(time.Now()) {
		delete(c.entries, key)
		ok = false
	}
	return entry, ok
}
