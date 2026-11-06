package model

import (
	"net/netip"
	"slices"
	"sort"
)

// Conn is one connection inside a ConnGroup.
type Conn struct {
	Proto  string         `json:"proto"`
	Local  netip.AddrPort `json:"local"`
	Remote netip.AddrPort `json:"remote"`
	State  string         `json:"state,omitempty"`
	PID    int            `json:"pid"`
	Rate   Rate           `json:"rate"`
}

// ConnGroup is one row on the Connections screen: an app and its connections.
type ConnGroup struct {
	App     string    `json:"app"`
	AppPath string    `json:"app_path,omitempty"`
	PIDs    []int     `json:"pids"`
	UID     int       `json:"uid"`
	Conns   []Conn    `json:"connections"`
	Rate    Rate      `json:"rate"`
	New     bool      `json:"new"`
	Main    ProcessID `json:"-"` // the app's main process (or the lone process), which quit and kill act on
}

// BuildConnGroups groups sockets that talk to a remote peer by app, busiest download first.
// Loopback connections are left out unless loopback is set.
func BuildConnGroups(socks []Socket, procs map[int]Process, rates map[SocketKey]Rate, loopback bool) []ConnGroup {
	mains := mainProcesses(procs)
	idx := map[string]int{}
	var out []ConnGroup
	for _, s := range socks {
		if !s.IsConnection() || (!loopback && s.Remote.Addr().IsLoopback()) {
			continue
		}
		p, ok := procs[s.PID]
		if !ok {
			p = Process{ProcessID: ProcessID{PID: s.PID, Name: s.Command}, UID: -1}
		}
		key, name, appPath := groupKey(p)
		i, ok := idx[key]
		if !ok {
			i = len(out)
			idx[key] = i
			ng := ConnGroup{App: name, AppPath: appPath, UID: p.UID, Main: p.ProcessID}
			if main, found := mains[appPath]; found {
				ng.Main, ng.UID = main.ProcessID, main.UID
			}
			out = append(out, ng)
		}
		g := &out[i]
		r := rates[KeyOf(s)]
		g.Conns = append(g.Conns, Conn{Proto: s.Proto, Local: s.Local, Remote: s.Remote, State: s.State, PID: s.PID, Rate: r})
		g.Rate.Rx += r.Rx
		g.Rate.Tx += r.Tx
		if !slices.Contains(g.PIDs, s.PID) {
			g.PIDs = append(g.PIDs, s.PID)
		}
	}
	for i := range out {
		conns := out[i].Conns
		sort.SliceStable(conns, func(a, b int) bool { return conns[a].Rate.Rx > conns[b].Rate.Rx })
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rate.Rx != out[j].Rate.Rx {
			return out[i].Rate.Rx > out[j].Rate.Rx
		}
		return out[i].App < out[j].App
	})
	return out
}

// mainProcesses maps each app bundle to its main executable's process, so quit and kill act on
// the app itself even when only a helper owns the connections. With several instances of one
// app, the lowest PID wins, so the choice doesn't change between refreshes.
func mainProcesses(procs map[int]Process) map[string]Process {
	mains := map[string]Process{}
	for _, p := range procs {
		a := AppPath(p.Path)
		if a == "" || !isMainExecutable(p.Path, a) {
			continue
		}
		if cur, ok := mains[a]; !ok || p.PID < cur.PID {
			mains[a] = p
		}
	}
	return mains
}

// Baseline remembers which apps had connections in the first snapshot, so later ones can be
// marked new.
type Baseline struct {
	seen map[string]bool
}

// Mark records the apps on its first call. On later calls it sets New on apps that weren't there.
func (b *Baseline) Mark(groups []ConnGroup) {
	if b.seen == nil {
		b.seen = map[string]bool{}
		for _, g := range groups {
			b.seen[g.App] = true
		}
		return
	}
	for i := range groups {
		groups[i].New = !b.seen[groups[i].App]
	}
}
