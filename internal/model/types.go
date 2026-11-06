// Package model holds soap's data types and every decision that doesn't need I/O.
package model

import (
	"errors"
	"net/netip"
	"strings"
	"time"
)

// ErrNoProcess means the process no longer exists.
var ErrNoProcess = errors.New("no such process")

// ErrUnavailable means an optional data source, such as Docker, isn't installed or running.
var ErrUnavailable = errors.New("not available")

// Socket is one TCP or UDP socket reported by netstat.
type Socket struct {
	Proto   string         `json:"proto"` // tcp4, tcp6, tcp46, udp4, udp6, udp46
	Local   netip.AddrPort `json:"local"`
	Remote  netip.AddrPort `json:"remote"` // the zero value when there's no peer
	State   string         `json:"state,omitempty"`
	PID     int            `json:"pid"`
	Command string         `json:"-"` // netstat's name for the process, cut to 16 characters
	RxBytes uint64         `json:"rx_bytes"`
	TxBytes uint64         `json:"tx_bytes"`
}

// Transport returns "tcp" or "udp".
func (s Socket) Transport() string {
	if strings.HasPrefix(s.Proto, "udp") {
		return "udp"
	}
	return "tcp"
}

// IsListener reports whether the socket waits for incoming traffic: a TCP socket in LISTEN
// state, or a UDP socket bound to a port with no peer.
func (s Socket) IsListener() bool {
	if s.Transport() == "tcp" {
		return s.State == "LISTEN"
	}
	return !s.Remote.IsValid() && s.Local.Port() != 0
}

// IsConnection reports whether the socket talks to a specific remote peer.
func (s Socket) IsConnection() bool {
	return s.Remote.IsValid() && s.Remote.Port() != 0 && s.State != "LISTEN"
}

// ProcessID is what the user saw on screen. Actions check it again before touching a process.
type ProcessID struct {
	PID       int       `json:"pid"`
	Name      string    `json:"name"`
	StartTime time.Time `json:"start_time"`
}

// Process is one running process.
type Process struct {
	ProcessID
	PPID   int     `json:"ppid"`
	UID    int     `json:"uid"`
	User   string  `json:"user"`
	Path   string  `json:"path,omitempty"`
	CPU    float64 `json:"cpu"`    // percent of one core
	Memory uint64  `json:"memory"` // bytes: the footprint when allowed, otherwise resident size
}

// Pressure is the kernel's memory pressure level.
type Pressure int

const (
	PressureUnknown Pressure = iota
	PressureNormal
	PressureWarning
	PressureCritical
)

// PressureFromLevel converts kern.memorystatus_vm_pressure_level (1, 2 or 4) to a Pressure.
func PressureFromLevel(level int) Pressure {
	switch level {
	case 1:
		return PressureNormal
	case 2:
		return PressureWarning
	case 4:
		return PressureCritical
	}
	return PressureUnknown
}

func (p Pressure) String() string {
	switch p {
	case PressureNormal:
		return "normal"
	case PressureWarning:
		return "warning"
	case PressureCritical:
		return "critical"
	}
	return "unknown"
}

// MarshalText makes Pressure appear as a word in JSON.
func (p Pressure) MarshalText() ([]byte, error) { return []byte(p.String()), nil }

// MemoryStats are machine-wide memory figures in bytes, matching Activity Monitor.
type MemoryStats struct {
	Total      uint64   `json:"total"`
	Used       uint64   `json:"used"`
	Cached     uint64   `json:"cached"`
	Compressed uint64   `json:"compressed"`
	SwapUsed   uint64   `json:"swap_used"`
	Pressure   Pressure `json:"pressure"`
}

// Container is a running Docker container.
type Container struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Ports []PublishedPort `json:"ports"`
}

// PublishedPort is a host port that Docker forwards into a container.
type PublishedPort struct {
	HostPort uint16 `json:"host_port"`
	Proto    string `json:"proto"` // "tcp" or "udp"
}

// IfCounters are a network interface's byte counters. macOS keeps them as 32-bit values that wrap.
type IfCounters struct {
	In, Out uint32
}

// Rate is a pair of bytes-per-second figures.
type Rate struct {
	Rx float64 `json:"rx_bytes_per_sec"`
	Tx float64 `json:"tx_bytes_per_sec"`
}
