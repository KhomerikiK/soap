package model

import (
	"net/netip"
	"testing"
	"time"
)

func TestTrafficTracker(t *testing.T) {
	ap := netip.MustParseAddrPort
	s := Socket{Proto: "tcp4", Local: ap("10.0.0.2:1"), Remote: ap("1.1.1.1:443"), State: "ESTABLISHED", PID: 7, RxBytes: 1000, TxBytes: 100}
	var tr TrafficTracker
	if r := tr.Update([]Socket{s}, t0); len(r) != 0 {
		t.Errorf("first sample has no rates, got %v", r)
	}
	s.RxBytes, s.TxBytes = 3048, 612
	r := tr.Update([]Socket{s}, t0.Add(2*time.Second))
	if got := r[KeyOf(s)]; got.Rx != 1024 || got.Tx != 256 {
		t.Errorf("rate = %+v, want 1024/256", got)
	}
	s.RxBytes = 10 // the counter went down: a new socket reused the same addresses
	if r := tr.Update([]Socket{s}, t0.Add(4*time.Second)); len(r) != 0 {
		t.Errorf("got %v, want no rate for a reset counter", r)
	}
}

func TestCPUTracker(t *testing.T) {
	var c CPUTracker
	if got := c.Update(100, 1000); got != 0 {
		t.Errorf("first sample = %v, want 0", got)
	}
	if got := c.Update(150, 1200); got != 25 {
		t.Errorf("second sample = %v, want 25", got)
	}
}

func TestNetTrackerHandlesWrap(t *testing.T) {
	var n NetTracker
	n.Update(map[string]IfCounters{"en0": {In: 4294967000, Out: 10}}, t0)
	r := n.Update(map[string]IfCounters{"en0": {In: 200, Out: 1034}}, t0.Add(time.Second))
	if r.Rx != 496 || r.Tx != 1024 {
		t.Errorf("rate = %+v, want 496/1024", r)
	}
}
