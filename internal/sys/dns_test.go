package sys

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func waitFor(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("lookup didn't finish")
	}
}

func TestResolverReturnsNameAfterLookup(t *testing.T) {
	var calls atomic.Int32
	r := newResolver(func(context.Context, string) ([]string, error) {
		calls.Add(1)
		return []string{"lhr48s29-in-f14.1e100.net."}, nil
	}, time.Now, time.Second, 8)
	done := make(chan struct{}, 4)
	r.OnResolve = func() { done <- struct{}{} }
	ip := netip.MustParseAddr("142.250.185.78")
	if got := r.Name(ip); got != "" {
		t.Fatalf("first call = %q, want empty while looking up", got)
	}
	waitFor(t, done)
	if got := r.Name(ip); got != "lhr48s29-in-f14.1e100.net" {
		t.Errorf("Name = %q", got)
	}
	if calls.Load() != 1 {
		t.Errorf("looked up %d times, want 1", calls.Load())
	}
}

func TestResolverCachesFailuresForTenMinutes(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)}
	var calls atomic.Int32
	r := newResolver(func(context.Context, string) ([]string, error) {
		calls.Add(1)
		return nil, errors.New("no PTR record")
	}, clock.Now, time.Second, 8)
	done := make(chan struct{}, 4)
	r.OnResolve = func() { done <- struct{}{} }
	ip := netip.MustParseAddr("151.101.1.69")
	r.Name(ip)
	waitFor(t, done)
	clock.Advance(9 * time.Minute)
	if got := r.Name(ip); got != "" {
		t.Errorf("Name = %q, want empty", got)
	}
	if calls.Load() != 1 {
		t.Fatalf("looked up %d times within 10 minutes, want 1", calls.Load())
	}
	clock.Advance(2 * time.Minute)
	r.Name(ip)
	waitFor(t, done)
	if calls.Load() != 2 {
		t.Errorf("looked up %d times after 11 minutes, want 2", calls.Load())
	}
}

func TestResolverLimitsConcurrentLookups(t *testing.T) {
	var cur, peak atomic.Int32
	release := make(chan struct{})
	lookup := func(context.Context, string) ([]string, error) {
		n := cur.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		<-release
		cur.Add(-1)
		return nil, errors.New("no PTR record")
	}
	r := newResolver(lookup, time.Now, 5*time.Second, 8)
	var wg sync.WaitGroup
	wg.Add(20)
	r.OnResolve = wg.Done
	for i := range 20 {
		r.Name(netip.AddrFrom4([4]byte{10, 0, 0, byte(i + 1)}))
	}
	time.Sleep(200 * time.Millisecond)
	close(release)
	wg.Wait()
	if got := peak.Load(); got != 8 {
		t.Errorf("peak concurrent lookups = %d, want 8", got)
	}
}
