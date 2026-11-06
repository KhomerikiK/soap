package model

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
)

func TestSocketKinds(t *testing.T) {
	ap := netip.MustParseAddrPort
	tests := []struct {
		name       string
		s          Socket
		listener   bool
		connection bool
	}{
		{"tcp listen", Socket{Proto: "tcp4", Local: ap("127.0.0.1:631"), State: "LISTEN"}, true, false},
		{"tcp established", Socket{Proto: "tcp4", Local: ap("10.0.0.2:51727"), Remote: ap("104.18.18.125:443"), State: "ESTABLISHED"}, false, true},
		{"udp bound", Socket{Proto: "udp4", Local: ap("0.0.0.0:5353")}, true, false},
		{"udp connected", Socket{Proto: "udp4", Local: ap("10.0.0.2:54248"), Remote: ap("203.0.113.143:443")}, false, true},
		{"udp unbound", Socket{Proto: "udp46"}, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.IsListener(); got != tt.listener {
				t.Errorf("IsListener() = %v, want %v", got, tt.listener)
			}
			if got := tt.s.IsConnection(); got != tt.connection {
				t.Errorf("IsConnection() = %v, want %v", got, tt.connection)
			}
		})
	}
}

func TestTransport(t *testing.T) {
	for proto, want := range map[string]string{"tcp4": "tcp", "tcp46": "tcp", "udp6": "udp", "udp46": "udp"} {
		if got := (Socket{Proto: proto}).Transport(); got != want {
			t.Errorf("Transport(%s) = %s, want %s", proto, got, want)
		}
	}
}

func TestPressureFromLevel(t *testing.T) {
	want := map[int]Pressure{1: PressureNormal, 2: PressureWarning, 4: PressureCritical, 0: PressureUnknown, 3: PressureUnknown}
	for level, w := range want {
		if got := PressureFromLevel(level); got != w {
			t.Errorf("PressureFromLevel(%d) = %v, want %v", level, got, w)
		}
	}
}

func TestPressureJSON(t *testing.T) {
	b, err := json.Marshal(MemoryStats{Pressure: PressureWarning})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"pressure":"warning"`) {
		t.Errorf("got %s, want pressure as a word", b)
	}
}
