package suppressor

import (
	"runtime"
	"sync/atomic"
	"time"

	"github.com/moeryomenko/synx"
)

// Cache is common interface of cache.
type Cache[K comparable, V any] interface {
	// SetNX inserts or updates the specified key-value pair with an expiration time.
	SetNX(key K, value V, expiry time.Duration)
	// Get returns the value for specified key if it is present in the cache.
	Get(key K) (V, bool)
}

// Suppressor represents a class of work and forms a namespace in
// which units of work can be executed with duplicate suppression.
type Suppressor[K comparable, V any] struct {
	ttl        time.Duration
	cached     Cache[K, Result[V]]
	mu         synx.Spinlock
	awaitLocks map[K]*int32
}

func New[K comparable, V any](ttl time.Duration, cache Cache[K, Result[V]]) *Suppressor[K, V] {
	return &Suppressor[K, V]{
		cached:     cache,
		ttl:        ttl,
		awaitLocks: make(map[K]*int32),
	}
}

// Result holds the results of Do, so they can be passed
// on a channel.
type Result[V any] struct {
	Val V
	Err error
}

// Do executes and returns the results of the given function, making
// sure that only one execution is in-flight for a given key at a
// time. If a duplicate comes in, the duplicate caller waits for the
// original to complete and receives the same results.
// The return a channel that will receive the
// results when they are ready.
//
// The returned channel will not be closed.
func (g *Suppressor[K, V]) Do(key K, fn func() (V, error)) Result[V] {
	if val, ok := g.cached.Get(key); ok {
		return val
	}

	return g.onceDo(key, fn)
}

func (g *Suppressor[K, V]) onceDo(key K, fn func() (V, error)) Result[V] {
	lock, ok := g.checkExecuted(key)

	// subscribe on result.
	if ok {
		// NOTE: if result not ready yield this goroutine.
		for i := 0; atomic.LoadInt32(lock) == 1; {
			if i < 2 {
				i++
				time.Sleep(g.ttl / 20)
				continue
			}

			runtime.Gosched()
		}
		val, _ := g.cached.Get(key)
		return val
	}

	result := Result[V]{}
	result.Val, result.Err = fn()
	g.cached.SetNX(key, result, g.ttl)
	atomic.StoreInt32(lock, 0)

	go func(key K) {
		time.AfterFunc(g.ttl, func() {
			g.releaseAwaitLock(key)
		})
	}(key)

	return result
}

// checkExecuted return await lock descriptor.
func (g *Suppressor[K, V]) checkExecuted(key K) (*int32, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	lock, ok := g.awaitLocks[key]
	if !ok {
		g.awaitLocks[key] = intRef(1)
		return g.awaitLocks[key], false
	}
	return lock, true
}

func (g *Suppressor[K, V]) releaseAwaitLock(key K) {
	g.mu.Lock()
	delete(g.awaitLocks, key)
	g.mu.Unlock()
}

func intRef(i int32) *int32 {
	return &i
}
