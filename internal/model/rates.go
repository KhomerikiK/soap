package model

import (
	"net/netip"
	"time"
)

// SocketKey identifies a socket across two netstat samples.
type SocketKey struct {
	Proto         string
	Local, Remote netip.AddrPort
	PID           int
}

// KeyOf returns the key for s.
func KeyOf(s Socket) SocketKey {
	return SocketKey{Proto: s.Proto, Local: s.Local, Remote: s.Remote, PID: s.PID}
}

// TrafficTracker turns netstat's running byte totals into per-socket rates.
type TrafficTracker struct {
	prev map[SocketKey][2]uint64
	at   time.Time
}

// Update records a sample taken at now and returns bytes per second for every socket seen in
// both samples. A socket whose counters went down is a new socket and gets no rate yet.
func (t *TrafficTracker) Update(socks []Socket, now time.Time) map[SocketKey]Rate {
	cur := make(map[SocketKey][2]uint64, len(socks))
	rates := map[SocketKey]Rate{}
	dt := now.Sub(t.at).Seconds()
	for _, s := range socks {
		k := KeyOf(s)
		cur[k] = [2]uint64{s.RxBytes, s.TxBytes}
		p, ok := t.prev[k]
		if !ok || dt <= 0 || s.RxBytes < p[0] || s.TxBytes < p[1] {
			continue
		}
		rates[k] = Rate{Rx: float64(s.RxBytes-p[0]) / dt, Tx: float64(s.TxBytes-p[1]) / dt}
	}
	t.prev, t.at = cur, now
	return rates
}

// CPUTracker turns CPU tick totals into a usage percentage for the whole machine.
type CPUTracker struct {
	busy, total uint64
	ok          bool
}

// Update records a sample and returns the percentage busy since the previous one (0 on the first).
func (c *CPUTracker) Update(busy, total uint64) float64 {
	prevBusy, prevTotal, had := c.busy, c.total, c.ok
	c.busy, c.total, c.ok = busy, total, true
	if !had || total <= prevTotal || busy < prevBusy {
		return 0
	}
	return float64(busy-prevBusy) / float64(total-prevTotal) * 100
}

// NetTracker turns interface byte counters into a machine-wide rate.
type NetTracker struct {
	prev map[string]IfCounters
	at   time.Time
}

// Update records a sample and returns bytes per second since the previous one. The counters are
// 32-bit and wrap, so the difference is taken as uint32.
func (n *NetTracker) Update(cur map[string]IfCounters, now time.Time) Rate {
	var r Rate
	dt := now.Sub(n.at).Seconds()
	if n.prev != nil && dt > 0 {
		for name, c := range cur {
			if p, ok := n.prev[name]; ok {
				r.Rx += float64(c.In-p.In) / dt
				r.Tx += float64(c.Out-p.Out) / dt
			}
		}
	}
	n.prev, n.at = cur, now
	return r
}
