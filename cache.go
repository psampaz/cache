package cache

import (
	"context"
	"io"
	"time"
)

const (
	// DefaultTTL indicates default value (replaced by config.TimeToLive) for entry expiration time.
	DefaultTTL = time.Duration(0)

	// UnlimitedTTL indicates unlimited TTL for config TimeToLive.
	UnlimitedTTL = time.Duration(-1)
)

// Reader reads from cache.
type Reader interface {
	// Read returns cached value or error.
	Read(ctx context.Context, key []byte) (interface{}, error)
}

// Writer writes to cache.
type Writer interface {
	// Write stores value in cache with a given key.
	Write(ctx context.Context, key []byte, value interface{}) error
}

// Deleter deletes from cache.
type Deleter interface {
	// Delete removes a cache entry with a given key
	// and returns ErrNotFound for non-existent keys.
	Delete(ctx context.Context, key []byte) error
}

// ReadWriter reads from and writes to cache.
type ReadWriter interface {
	Reader
	Writer
}

// Entry is cache entry with key and value.
type Entry interface {
	Key() []byte
	Value() interface{}
	ExpireAt() time.Time
}

// Walker calls function for every entry in cache and fails on first error returned by that function.
//
// Count of processed entries is returned.
type Walker interface {
	Walk(func(entry Entry) error) (int, error)
}

// Dumper dumps cache entries in binary format.
type Dumper interface {
	Dump(w io.Writer) (int, error)
}

// Restorer restores cache entries from binary dump.
type Restorer interface {
	Restore(r io.Reader) (int, error)
}

// WalkDumpRestorer walks, dumps and restores cache.
type WalkDumpRestorer interface {
	Dumper
	Walker
	Restorer
}
