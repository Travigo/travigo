package identifiers

import (
	"container/list"
	"encoding/json"
	"sync"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"golang.org/x/sync/singleflight"
)

var (
	siriOperators = lookupCache[*ctdf.Operator]{limit: 512, ttl: 5 * time.Minute}
	siriServices  = lookupCache[[]string]{limit: 2048, ttl: 5 * time.Minute}
	siriJourneys  = lookupCache[string]{limit: 8192, ttl: 5 * time.Minute, missTTL: 2 * time.Minute}
	// Cache empty reference lookups, without retaining candidate journey paths.
	siriReferenceMisses = lookupCache[struct{}]{limit: 8192, ttl: 2 * time.Minute}
)

type siriCacheEntry[T any] struct {
	key   string
	value T
	err   error
	until time.Time
}

type lookupCache[T any] struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	recent  list.List
	loads   singleflight.Group
	limit   int
	ttl     time.Duration
	missTTL time.Duration
}

func lookupKey(parts ...string) string {
	key, _ := json.Marshal(parts)
	return string(key)
}

func (c *lookupCache[T]) get(key string, load func() (T, error)) (T, error) {
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
		return c.store(key, value, err), nil
	})
	if err != nil {
		var zero T
		return zero, err
	}
	entry := result.(siriCacheEntry[T])
	return entry.value, entry.err
}

func (c *lookupCache[T]) store(key string, value T, err error) siriCacheEntry[T] {
	ttl := c.ttl
	if err != nil {
		ttl = c.missTTL
		if ttl == 0 {
			ttl = time.Minute
		}
	}
	entry := siriCacheEntry[T]{key: key, value: value, err: err, until: time.Now().Add(ttl)}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]*list.Element)
	}
	if previous, ok := c.entries[key]; ok {
		c.recent.Remove(previous)
	} else if len(c.entries) >= c.limit {
		oldest := c.recent.Back()
		delete(c.entries, oldest.Value.(siriCacheEntry[T]).key)
		c.recent.Remove(oldest)
	}
	c.entries[key] = c.recent.PushFront(entry)
	return entry
}

func (c *lookupCache[T]) lookup(key string) (siriCacheEntry[T], bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return siriCacheEntry[T]{}, false
	}
	entry := element.Value.(siriCacheEntry[T])
	if !entry.until.After(time.Now()) {
		delete(c.entries, key)
		c.recent.Remove(element)
		return siriCacheEntry[T]{}, false
	}
	c.recent.MoveToFront(element)
	return entry, true
}
