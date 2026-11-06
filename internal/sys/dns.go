package sys

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

const (
	dnsHitTTL  = time.Hour
	dnsMissTTL = 10 * time.Minute
)

// Resolver looks up hostnames for IP addresses in the background and caches the answers, so the
// TUI never waits on DNS.
type Resolver struct {
	// OnResolve, when set, is called after each lookup finishes so the UI can redraw.
	OnResolve func()

	lookup  func(ctx context.Context, addr string) ([]string, error)
	now     func() time.Time
	timeout time.Duration
	sem     chan struct{}

	mu      sync.Mutex
	cache   map[netip.Addr]dnsEntry
	pending map[netip.Addr]bool
}

type dnsEntry struct {
	name    string
	expires time.Time
}

// NewResolver uses the system resolver with a 2-second timeout per lookup and at most 8 lookups
// at once.
func NewResolver() *Resolver {
	return newResolver(net.DefaultResolver.LookupAddr, time.Now, 2*time.Second, 8)
}

func newResolver(lookup func(context.Context, string) ([]string, error), now func() time.Time, timeout time.Duration, limit int) *Resolver {
	return &Resolver{
		lookup:  lookup,
		now:     now,
		timeout: timeout,
		sem:     make(chan struct{}, limit),
		cache:   map[netip.Addr]dnsEntry{},
		pending: map[netip.Addr]bool{},
	}
}

// Name returns the cached hostname for ip, or "" when there's none yet. A miss or an expired
// entry starts a background lookup; an expired name is still returned until it's replaced.
// Names are cached for an hour, failures for 10 minutes.
func (r *Resolver) Name(ip netip.Addr) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.cache[ip]
	if ok && r.now().Before(e.expires) {
		return e.name
	}
	if !r.pending[ip] {
		r.pending[ip] = true
		go r.resolve(ip)
	}
	return e.name
}

func (r *Resolver) resolve(ip netip.Addr) {
	r.sem <- struct{}{}
	defer func() { <-r.sem }()
	ctx, cancel := context.WithTimeout(context.Background(), r.timeout)
	defer cancel()
	names, err := r.lookup(ctx, ip.String())
	e := dnsEntry{expires: r.now().Add(dnsMissTTL)}
	if err == nil && len(names) > 0 {
		e = dnsEntry{name: strings.TrimSuffix(names[0], "."), expires: r.now().Add(dnsHitTTL)}
	}
	r.mu.Lock()
	r.cache[ip] = e
	delete(r.pending, ip)
	onResolve := r.OnResolve
	r.mu.Unlock()
	if onResolve != nil {
		onResolve()
	}
}
