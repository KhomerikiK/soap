package model

import (
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Listener is one row on the Ports screen: a process listening on a port over TCP or UDP.
type Listener struct {
	Port      uint16    `json:"port"`
	Proto     string    `json:"proto"` // tcp4, tcp6, tcp46, udp4, udp6, udp46
	PID       int       `json:"pid"`
	Process   string    `json:"process"`
	User      string    `json:"user"`
	UID       int       `json:"uid"`  // -1 when the process exited before ps saw it
	Bind      string    `json:"bind"` // "*" or a comma-separated list of addresses
	Note      string    `json:"note,omitempty"`
	Container string    `json:"container,omitempty"`
	ID        ProcessID `json:"-"`
	Engine    bool      `json:"-"` // the process is Docker Desktop or OrbStack, which forwards the port
	System    bool      `json:"-"` // the process ships with macOS (IsSystem); false when ps didn't see it
}

// EngineRefusal is why soap won't stop a container engine's process from a port row when no
// container is known to publish the port: stopping the engine would stop every container.
func EngineRefusal(port uint16) string {
	return fmt.Sprintf("Docker owns :%d but no container is known for it. Nothing was stopped.", port)
}

// BuildListeners turns sockets into one row per process, port and transport, sorted by port.
// IPv4 and IPv6 sockets for the same process and port become one row. When Docker publishes the
// port, Container names the container; otherwise Note describes what a script runner runs.
func BuildListeners(socks []Socket, procs map[int]Process, containers []Container, args map[int]string) []Listener {
	type key struct {
		pid       int
		port      uint16
		transport string
	}
	type acc struct {
		l      Listener
		v4, v6 bool
		binds  []string
	}
	published := map[PublishedPort]string{}
	for _, c := range containers {
		for _, p := range c.Ports {
			published[p] = c.Name
		}
	}
	byKey := map[key]*acc{}
	var order []key
	for _, s := range socks {
		if !s.IsListener() {
			continue
		}
		k := key{s.PID, s.Local.Port(), s.Transport()}
		a, ok := byKey[k]
		if !ok {
			a = &acc{l: newListener(s, procs, k.port)}
			byKey[k] = a
			order = append(order, k)
		}
		switch {
		case strings.HasSuffix(s.Proto, "46"):
			a.v4, a.v6 = true, true
		case strings.HasSuffix(s.Proto, "6"):
			a.v6 = true
		default:
			a.v4 = true
		}
		bind := "*"
		if !s.Local.Addr().IsUnspecified() {
			bind = s.Local.Addr().String()
		}
		if !slices.Contains(a.binds, bind) {
			a.binds = append(a.binds, bind)
		}
	}
	// When an engine row shares a published port, only engine rows get the container, so a native
	// process on the same port keeps its own row.
	engineOn := map[PublishedPort]bool{}
	for _, k := range order {
		if byKey[k].l.Engine {
			engineOn[PublishedPort{HostPort: k.port, Proto: k.transport}] = true
		}
	}
	out := make([]Listener, 0, len(order))
	for _, k := range order {
		a := byKey[k]
		pp := PublishedPort{HostPort: k.port, Proto: k.transport}
		if !engineOn[pp] || a.l.Engine {
			a.l.Container = published[pp]
		}
		if a.l.Container == "" {
			a.l.Note = NoteFromArgs(args[k.pid])
		}
		a.l.Proto = k.transport + family(a.v4, a.v6)
		if slices.Contains(a.binds, "*") {
			a.l.Bind = "*"
		} else {
			a.l.Bind = strings.Join(a.binds, ", ")
		}
		out = append(out, a.l)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].PID < out[j].PID
	})
	return out
}

func newListener(s Socket, procs map[int]Process, port uint16) Listener {
	l := Listener{Port: port, PID: s.PID, Process: s.Command, UID: -1, ID: ProcessID{PID: s.PID, Name: s.Command}}
	if p, ok := procs[s.PID]; ok {
		l.Process, l.User, l.UID, l.ID = p.Name, p.User, p.UID, p.ProcessID
		l.Engine = IsContainerEngine(p)
		l.System = IsSystem(p)
	}
	return l
}

func family(v4, v6 bool) string {
	switch {
	case v4 && v6:
		return "46"
	case v6:
		return "6"
	}
	return "4"
}

var scriptRunners = map[string]bool{
	"node": true, "npm": true, "npx": true, "pnpm": true, "yarn": true, "bun": true, "deno": true,
	"php": true, "python": true, "python3": true, "ruby": true, "java": true,
}

var scriptExts = map[string]bool{".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".py": true, ".php": true, ".rb": true}

// NoteFromArgs labels script runners such as node or php with what they run, for example
// "next dev" or "artisan serve". It returns "" for other programs.
func NoteFromArgs(args string) string {
	f := strings.Fields(args)
	if len(f) < 2 || !scriptRunners[filepath.Base(f[0])] {
		return ""
	}
	var words []string
	for _, w := range f[1:] {
		if strings.HasPrefix(w, "-") {
			if len(words) > 0 {
				break
			}
			continue
		}
		w = filepath.Base(w)
		if ext := filepath.Ext(w); scriptExts[ext] {
			w = strings.TrimSuffix(w, ext)
		}
		if len(words) == 0 && scriptRunners[w] {
			continue
		}
		words = append(words, w)
		if len(words) == 2 {
			break
		}
	}
	return strings.Join(words, " ")
}
