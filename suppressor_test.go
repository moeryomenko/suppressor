package suppressor

import (
	"context"
	"errors"
	"fmt"

	//"fmt"
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
	g := New(200*time.Millisecond, &dummyTTLCache[string, Result[string]]{
		items:       make(map[string]Result[string]),
		expirations: make(map[string]time.Time),
	})

	var calls atomic.Int32

	fn := func() (string, error) {
		calls.Add(1)
		<-time.After(10 * time.Millisecond)
		return `test`, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	concurrent := 5

	group := synx.NewCtxGroup(ctx)

	for i := 0; i < concurrent; i++ {
		i := i
		group.Go(func(ctx context.Context) error {
			start := time.Now()
			defer func() {
				fmt.Printf("duration of gorotine: %s\n", time.Since(start))
			}()

			result := g.Do(`test`, fn)
			if result.Val != `test` || result.Err != nil {
				t.Logf(`invalid value returned: corotine_%d`, i)
				t.Fail()
			}
			return nil
		})
	}

	err := group.Wait()
	if err != nil {
		t.Fatal(fmt.Printf("group failed: %s", err))
	}

	if calls.Load() != 1 {
		t.Errorf(`unexpected calls count: %d`, calls.Load())
	}

	<-time.After(200 * time.Millisecond)

	result := g.Do(`test`, fn)
	if result.Val != `test` {
		t.Fatal(`invalid value returned`)
	}
	if calls.Load() != 2 {
		t.Fatalf(`unexpected calls count: %d`, calls.Load())
	}
}
