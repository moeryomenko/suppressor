// ported from https://github.com/moeryomenko/synx
package suppressor

import (
	"runtime"
	"sync/atomic"
	"time"

	"github.com/moeryomenko/synx"
)

// Cache is common interface of cache.
type Cache interface {
	// Set inserts or updates the specified key-value pair with an expiration time.
	Set(key string, value interface{}, expiry time.Duration) error
	// Get returns the value for specified key if it is present in the cache.
	Get(key string) (interface{}, error)
}

// Suppressor represents a class of work and forms a namespace in
// which units of work can be executed with duplicate suppression.
type Suppressor struct {
	ttl        time.Duration
	cached     Cache
	mu         synx.Spinlock
	awaitLocks map[string]*int32
}

func New(ttl time.Duration, cache Cache) *Suppressor {
	return &Suppressor{
		cached:     cache,
		ttl:        ttl,
		awaitLocks: make(map[string]*int32),
	}
}

// Result holds the results of Do, so they can be passed
// on a channel.
type Result struct {
	Val interface{}
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
func (g *Suppressor) Do(key string, fn func() (interface{}, error)) Result {
	val, err := g.cached.Get(key)
	if err == nil {
		return val.(Result)
	}

	return g.onceDo(key, fn)
}

func (g *Suppressor) onceDo(key string, fn func() (any, error)) Result {
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
		return val.(Result)
	}

	result := Result{}
	result.Val, result.Err = fn()
	_ = g.cached.Set(key, result, g.ttl)
	atomic.StoreInt32(lock, 0)

	go func(key string) {
		time.AfterFunc(g.ttl, func() {
			g.releaseAwaitLock(key)
		})
	}(key)

	return result
}

// checkExecuted return await lock descriptor.
func (g *Suppressor) checkExecuted(key string) (*int32, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	lock, ok := g.awaitLocks[key]
	if !ok {
		g.awaitLocks[key] = intRef(1)
		return g.awaitLocks[key], false
	}
	return lock, true
}

func (g *Suppressor) releaseAwaitLock(key string) {
	g.mu.Lock()
	delete(g.awaitLocks, key)
	g.mu.Unlock()
}

func intRef(i int32) *int32 {
	return &i
}
