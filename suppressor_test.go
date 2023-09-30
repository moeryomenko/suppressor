package suppressor

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moeryomenko/synx"
)

var errNotFound = errors.New(`not found`)

type dummyTTLCache[K comparable, V any] struct {
	items       map[K]V
	expirations map[K]time.Time
	lock        synx.Spinlock
}

func (c *dummyTTLCache[K, V]) SetNX(key K, value V, expiry time.Duration) {
	c.lock.Lock()
	c.items[key] = value
	c.expirations[key] = time.Now().Add(expiry)
	c.lock.Unlock()
}

func (c *dummyTTLCache[K, V]) Get(key K) (V, bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	value, ok := c.items[key]
	if !ok {
		var v V
		return v, false
	}

	if c.expirations[key].Before(time.Now()) {
		delete(c.items, key)
		delete(c.expirations, key)
		var v V
		return v, false
	}

	return value, ok
}

func TestDoDeduplicate(t *testing.T) {
	g := New(100*time.Millisecond, &dummyTTLCache[string, Result[string]]{
		items:       make(map[string]Result[string]),
		expirations: make(map[string]time.Time),
	})

	var calls int32

	fn := func() (string, error) {
		atomic.AddInt32(&calls, 1)
		<-time.After(80 * time.Millisecond)
		return `test`, nil
	}

	ticker := time.NewTicker(10 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	concurrent := 100

	group := synx.NewCtxGroup(ctx)

loop:
	for {
		select {
		case <-ticker.C:
			for i := 0; i < concurrent; i++ {
				group.Go(func(ctx context.Context) error {
					result := g.Do(`test`, fn)
					if result.Val != `test` {
						t.Log(`invalid value returned`)
						t.Fail()
					}
					return nil
				})
			}
		case <-ctx.Done():
			ticker.Stop()
			break loop
		}
	}

	group.Wait()

	if calls != 1 {
		t.Errorf(`unexpected calls count: %d`, calls)
	}

	<-time.After(200 * time.Millisecond)

	result := g.Do(`test`, fn)
	if  result.Val != `test` {
		t.Fatal(`invalid value returned`)
	}
	if calls != 2 {
		t.Fatalf(`unexpected calls count: %d`, calls)
	}
}
