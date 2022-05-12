package cache

import (
	"context"
	"encoding/gob"
	"errors"
	"io"
	"runtime"
	"sort"
	"sync"
	"time"
)

var (
	_ ReadWriter = &syncMap{}
	_ Deleter    = &syncMap{}
)

// SyncMap is an in-memory cache backend. Please use NewSyncMap to create it.
type SyncMap struct {
	*syncMap
}

type syncMap struct {
	data sync.Map

	t *trait
}

// NewSyncMap creates an instance of in-memory cache with optional configuration.
func NewSyncMap(options ...func(cfg *Config)) *SyncMap {
	c := &syncMap{}
	C := &SyncMap{
		syncMap: c,
	}

	cfg := Config{}
	for _, option := range options {
		option(&cfg)
	}

	c.t = newTrait(c, cfg)

	runtime.SetFinalizer(C, func(m *SyncMap) {
		close(m.t.Closed)
	})

	return C
}

// Read gets value.
func (c *syncMap) Read(ctx context.Context, key []byte) (interface{}, error) {
	if SkipRead(ctx) {
		return nil, ErrNotFound
	}

	if cacheEntry, found := c.data.Load(string(key)); found {
		return c.t.prepareRead(ctx, cacheEntry.(*entry), true)
	}

	return c.t.prepareRead(ctx, nil, false)
}

// Write sets value by the key.
func (c *syncMap) Write(ctx context.Context, k []byte, v interface{}) error {
	ttl := c.t.TTL(ctx)

	// Copy key to allow mutations of original argument.
	key := make([]byte, len(k))
	copy(key, k)

	c.data.Store(string(k), &entry{V: v, K: key, E: time.Now().Add(ttl)})
	c.t.NotifyWritten(ctx, key, v, ttl)

	return nil
}

// Delete removes values by the key.
func (c *syncMap) Delete(ctx context.Context, key []byte) error {
	c.data.Delete(string(key))

	c.t.NotifyDeleted(ctx, key)

	return nil
}

// ExpireAll marks all entries as expired, they can still serve stale values.
func (c *syncMap) ExpireAll(ctx context.Context) {
	start := time.Now()
	cnt := 0

	c.data.Range(func(key, value interface{}) bool {
		cacheEntry := value.(*entry) // nolint // Panic on type assertion failure is fine here.

		cacheEntry.E = start
		cnt++

		return true
	})

	c.t.NotifyExpiredAll(ctx, start, cnt)
}

// DeleteAll erases all entries.
func (c *syncMap) DeleteAll(ctx context.Context) {
	start := time.Now()
	cnt := 0

	c.data.Range(func(key, _ interface{}) bool {
		c.data.Delete(key)
		cnt++

		return true
	})

	c.t.NotifyDeletedAll(ctx, start, cnt)
}

func (c *syncMap) deleteExpiredBefore(expirationBoundary time.Time) {
	c.data.Range(func(key, value interface{}) bool {
		cacheEntry := value.(*entry) // nolint // Panic on type assertion failure is fine here.
		if cacheEntry.E.Before(expirationBoundary) {
			c.data.Delete(key)
		}

		return true
	})

	if c.heapInUseOverflow() || c.countOverflow() {
		c.evictOldest()
	}
}

// Len returns number of elements including expired.
func (c *syncMap) Len() int {
	cnt := 0

	c.data.Range(func(key, value interface{}) bool {
		cnt++

		return true
	})

	return cnt
}

// Walk walks cached entries.
func (c *syncMap) Walk(walkFn func(e Entry) error) (int, error) {
	n := 0

	var lastErr error

	c.data.Range(func(key, value interface{}) bool {
		err := walkFn(value.(*entry))
		if err != nil {
			lastErr = err

			return false
		}

		n++

		return true
	})

	return n, lastErr
}

// Dump saves cached entries and returns a number of processed entries.
//
// Dump uses encoding/gob to serialize cache entries, therefore it is necessary to
// register cached types in advance with GobRegister.
func (c *SyncMap) Dump(w io.Writer) (int, error) {
	encoder := gob.NewEncoder(w)

	return c.Walk(func(e Entry) error {
		return encoder.Encode(e)
	})
}

// Restore loads cached entries and returns number of processed entries.
//
// Restore uses encoding/gob to unserialize cache entries, therefore it is necessary to
// register cached types in advance with GobRegister.
func (c *SyncMap) Restore(r io.Reader) (int, error) {
	var (
		decoder = gob.NewDecoder(r)
		e       entry
		n       = 0
	)

	for {
		err := decoder.Decode(&e)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return n, err
		}

		e := e

		c.data.Store(string(e.K), &e)

		n++
	}

	return n, nil
}

func (c *syncMap) evictOldest() {
	evictFraction := c.t.Config.EvictFraction
	if evictFraction == 0 {
		evictFraction = 0.1
	}

	type en struct {
		key      string
		expireAt time.Time
	}

	keysCnt := c.Len()
	entries := make([]en, 0, keysCnt)

	// Collect all keys and expirations.
	c.data.Range(func(key, value interface{}) bool {
		i := value.(*entry) // nolint // Panic on type assertion failure is fine here.
		entries = append(entries, en{expireAt: i.E, key: string(i.K)})

		return true
	})

	// Sort entries to put most expired in head.
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].expireAt.Before(entries[j].expireAt)
	})

	evictItems := int(float64(len(entries)) * evictFraction)

	if c.t.Stat != nil {
		c.t.Stat.Add(context.Background(), MetricEvict, float64(evictItems), "name", c.t.Config.Name)
	}

	for i := 0; i < evictItems; i++ {
		c.data.Delete(entries[i].key)
	}
}

func (c *syncMap) heapInUseOverflow() bool {
	if c.t.Config.HeapInUseSoftLimit == 0 {
		return false
	}

	m := runtime.MemStats{}
	runtime.ReadMemStats(&m)

	return m.HeapInuse >= c.t.Config.HeapInUseSoftLimit
}

func (c *syncMap) countOverflow() bool {
	if c.t.Config.CountSoftLimit == 0 {
		return false
	}

	return c.Len() >= int(c.t.Config.CountSoftLimit)
}
