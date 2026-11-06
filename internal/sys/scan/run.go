//go:build darwin

package scan

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/KhomerikiK/soap/internal/model"
)

// DevFinders are the finders behind the Dev screen.
func DevFinders() []Finder {
	return []Finder{
		{Group: "Xcode", Screen: model.ScreenDev, Find: findXcode},
		{Group: "Simulators", Screen: model.ScreenDev, Find: findSimulators},
		{Group: "Docker", Screen: model.ScreenDev, Find: findDocker},
		{Group: "Package caches", Screen: model.ScreenDev, Find: findPackages},
		{Group: "Projects", Screen: model.ScreenDev, Find: findProjects},
	}
}

// Finders returns the finders for a screen. Disk has none: it walks instead.
func Finders(screen model.Screen) []Finder {
	switch screen {
	case model.ScreenJunk:
		return JunkFinders()
	case model.ScreenDev:
		return DevFinders()
	case model.ScreenDupes:
		return DupesFinders()
	case model.ScreenApps:
		return AppsFinders()
	}
	return nil
}

// Run runs finders in parallel and sends one update per finder as each one finishes. A streaming
// finder also sends an update that clears what an earlier scan found, then one for each call to
// add; those updates and its last one have Add set. Items whose App is in running (lowercase
// process names), or whose Bundle has its model.BundleKey there, are marked busy. Once ctx is
// cancelled nothing more is sent. The channel is closed when every finder has returned.
func Run(ctx context.Context, env Env, finders []Finder, running map[string]bool) <-chan model.ScanUpdate {
	ch := make(chan model.ScanUpdate, len(finders))
	var mu sync.Mutex
	done := 0
	// send happens under the lock, so Done counts up in order. Any send can wait for the reader
	// once streamed updates fill the channel; it gives up when ctx is cancelled. So a reader must
	// keep reading until the channel closes, or cancel ctx.
	send := func(u model.ScanUpdate, last bool) {
		for i := range u.Items {
			if app := u.Items[i].App; app != "" && running[strings.ToLower(app)] {
				u.Items[i].Busy = true
			}
			if b := u.Items[i].Bundle; b != "" && running[model.BundleKey(b)] {
				u.Items[i].Busy = true
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if last {
			done++
		}
		if ctx.Err() != nil {
			return
		}
		u.Done, u.Total = done, len(finders)
		select {
		case ch <- u:
		case <-ctx.Done():
		}
	}
	var wg sync.WaitGroup
	for _, f := range finders {
		wg.Add(1)
		go func(f Finder) {
			defer wg.Done()
			var r Result
			if f.Stream != nil {
				send(model.ScanUpdate{Screen: f.Screen, Finder: f.Group}, false)
				r = stream(ctx, env, f, func(items []model.CleanItem, status string) {
					send(model.ScanUpdate{Screen: f.Screen, Finder: f.Group, Items: items, Status: status, Add: true}, false)
				})
			} else {
				r = find(ctx, env, f)
			}
			send(model.ScanUpdate{Screen: f.Screen, Finder: f.Group, Items: r.Items, Notes: r.Notes, Err: r.Err, Add: f.Stream != nil}, true)
		}(f)
	}
	go func() {
		wg.Wait()
		close(ch)
	}()
	return ch
}

// find runs one finder. A finder that panics fails on its own, like any other finder error.
func find(ctx context.Context, env Env, f Finder) (r Result) {
	defer func() {
		if p := recover(); p != nil {
			r = Result{Err: fmt.Errorf("the %s scan crashed: %v", f.Group, p)}
		}
	}()
	return f.Find(ctx, env)
}

// stream runs one streaming finder, as find runs the others.
func stream(ctx context.Context, env Env, f Finder, add func([]model.CleanItem, string)) (r Result) {
	defer func() {
		if p := recover(); p != nil {
			r = Result{Err: fmt.Errorf("the %s scan crashed: %v", f.Group, p)}
		}
	}()
	return f.Stream(ctx, env, add)
}
