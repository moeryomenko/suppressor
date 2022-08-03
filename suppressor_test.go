package suppressor

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	cache "github.com/moeryomenko/ttlcache"
	"golang.org/x/sync/errgroup"
)

func TestDoDeduplicate(t *testing.T) {
	g := New(10, 100*time.Millisecond, cache.LRU)

	var calls int32

	fn := func() (interface{}, error) {
		atomic.AddInt32(&calls, 1)
		<-time.After(40 * time.Millisecond)
		return `test`, nil
	}

	ticker := time.NewTicker(10 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	concurrent := 100

	group := errgroup.Group{}

loop:
	for {
		select {
		case <-ticker.C:
			for i := 0; i < concurrent; i++ {
				group.Go(func() error {
					result := g.Do(`test`, fn)
					if val, ok := result.Val.(string); !ok || val != `test` {
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

	<-time.After(100 * time.Millisecond)

	result := g.Do(`test`, fn)
	if val, ok := result.Val.(string); !ok || val != `test` {
		t.Fatal(`invalid value returned`)
	}
	if calls != 2 {
		t.Fatalf(`unexpected calls count: %d`, calls)
	}
}
