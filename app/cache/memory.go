// Package cache is the app's cache store. It replaces Goravel's memory
// driver, which forgets a key with a timer started by each Put or Add and
// never cancelled: a key written again before its first timer fires is
// still forgotten then, and a failed Add forgets the key someone else
// holds. The login limiter writes its bucket on every attempt, so that
// reset it early.
package cache

import (
	"context"
	"sync"
	"time"

	"github.com/spf13/cast"

	frameworkcache "github.com/goravel/framework/cache"
	contractscache "github.com/goravel/framework/contracts/cache"
	"github.com/goravel/framework/contracts/testing/docker"
	"github.com/goravel/framework/errors"
)

// sweepEvery is how often a write also drops expired entries, so keys
// nobody reads again don't pile up.
const sweepEvery = time.Minute

type entry struct {
	value   any
	expires time.Time // zero means never
}

// Memory keeps each entry's expiry with it, so the last write decides.
type Memory struct {
	mu        sync.Mutex
	entries   map[string]entry
	nextSweep time.Time
	now       func() time.Time
}

func NewMemory() *Memory {
	return &Memory{entries: map[string]entry{}, now: time.Now}
}

// load is the live entry for key. The caller holds mu.
func (r *Memory) load(key string) (entry, bool) {
	e, ok := r.entries[key]
	if ok && !e.expires.IsZero() && !r.now().Before(e.expires) {
		delete(r.entries, key)
		return entry{}, false
	}

	return e, ok
}

// store writes key. The caller holds mu.
func (r *Memory) store(key string, value any, t time.Duration) {
	now := r.now()
	if now.After(r.nextSweep) {
		for k, e := range r.entries {
			if !e.expires.IsZero() && !now.Before(e.expires) {
				delete(r.entries, k)
			}
		}
		r.nextSweep = now.Add(sweepEvery)
	}
	e := entry{value: value}
	if t != frameworkcache.NoExpiration {
		e.expires = now.Add(t)
	}
	r.entries[key] = e
}

func (r *Memory) Add(key string, value any, t time.Duration) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.load(key); ok {
		return false
	}
	r.store(key, value, t)

	return true
}

func (r *Memory) Increment(key string, value ...int64) (int64, error) {
	by := int64(1)
	if len(value) > 0 {
		by = value[0]
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	e, _ := r.load(key)
	n := int64(0)
	if e.value != nil {
		var err error
		if n, err = cast.ToInt64E(e.value); err != nil {
			return 0, errors.CacheMemoryInvalidIntValueType.Args(key)
		}
	}
	e.value = n + by
	r.entries[key] = e

	return n + by, nil
}

func (r *Memory) Decrement(key string, value ...int64) (int64, error) {
	by := int64(1)
	if len(value) > 0 {
		by = value[0]
	}

	return r.Increment(key, -by)
}

func (r *Memory) Docker() (docker.CacheDriver, error) {
	return nil, errors.CacheMemoryDriverNotSupportDocker
}

func (r *Memory) Forever(key string, value any) bool {
	return r.Put(key, value, frameworkcache.NoExpiration) == nil
}

func (r *Memory) Forget(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, key)

	return true
}

func (r *Memory) Flush() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = map[string]entry{}

	return true
}

func (r *Memory) Get(key string, def ...any) any {
	r.mu.Lock()
	e, ok := r.load(key)
	r.mu.Unlock()
	if ok {
		return e.value
	}
	if len(def) == 0 {
		return nil
	}
	if f, ok := def[0].(func() any); ok {
		return f()
	}

	return def[0]
}

func (r *Memory) GetBool(key string, def ...bool) bool {
	if len(def) == 0 {
		def = append(def, false)
	}

	return cast.ToBool(r.Get(key, def[0]))
}

func (r *Memory) GetInt(key string, def ...int) int {
	if len(def) == 0 {
		def = append(def, 0)
	}

	return cast.ToInt(r.Get(key, def[0]))
}

func (r *Memory) GetInt64(key string, def ...int64) int64 {
	if len(def) == 0 {
		def = append(def, 0)
	}

	return cast.ToInt64(r.Get(key, def[0]))
}

func (r *Memory) GetString(key string, def ...string) string {
	if len(def) == 0 {
		def = append(def, "")
	}

	return cast.ToString(r.Get(key, def[0]))
}

func (r *Memory) Has(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.load(key)

	return ok
}

// Lock is Goravel's lock, which takes and releases a key with Add and
// Forget.
func (r *Memory) Lock(key string, t ...time.Duration) contractscache.Lock {
	return frameworkcache.NewLock(r, key, t...)
}

func (r *Memory) Put(key string, value any, t time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.store(key, value, t)

	return nil
}

func (r *Memory) Pull(key string, def ...any) any {
	r.mu.Lock()
	e, ok := r.load(key)
	delete(r.entries, key)
	r.mu.Unlock()
	if ok {
		return e.value
	}
	if len(def) == 0 {
		return nil
	}

	return def[0]
}

func (r *Memory) Remember(key string, ttl time.Duration, callback func() (any, error)) (any, error) {
	if val := r.Get(key); val != nil {
		return val, nil
	}
	val, err := callback()
	if err != nil {
		return nil, err
	}

	return val, r.Put(key, val, ttl)
}

func (r *Memory) RememberForever(key string, callback func() (any, error)) (any, error) {
	return r.Remember(key, frameworkcache.NoExpiration, callback)
}

func (r *Memory) WithContext(context.Context) contractscache.Driver {
	return r
}
