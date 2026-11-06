//go:build darwin

package scan

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/KhomerikiK/soap/internal/model"
)

func TestRunStreamsEachFinderAndMarksBusy(t *testing.T) {
	release := make(chan struct{})
	finders := []Finder{
		{Group: "Fast", Screen: model.ScreenJunk, Find: func(context.Context, Env) Result {
			return Result{Items: []model.CleanItem{{ID: "a", Group: "Fast", App: "Xcode"}, {ID: "b", Group: "Fast"}}}
		}},
		{Group: "Slow", Screen: model.ScreenJunk, Find: func(context.Context, Env) Result {
			<-release
			return Result{Err: errors.New("broke")}
		}},
	}
	ch := Run(context.Background(), Env{}, finders, map[string]bool{"xcode": true})
	first := <-ch
	if first.Finder != "Fast" || first.Done != 1 || first.Total != 2 || !first.Items[0].Busy || first.Items[1].Busy {
		t.Errorf("first = %+v", first)
	}
	close(release)
	second := <-ch
	if second.Finder != "Slow" || second.Err == nil || second.Done != 2 {
		t.Errorf("second = %+v", second)
	}
	if _, open := <-ch; open {
		t.Error("the channel should close after the last finder")
	}
}

func TestRunSendsNothingOnceCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	finders := []Finder{{Group: "X", Find: func(context.Context, Env) Result { cancel(); return Result{} }}}
	if _, open := <-Run(ctx, Env{}, finders, nil); open {
		t.Error("a cancelled scan sends nothing")
	}
}

func TestFinders(t *testing.T) {
	var names []string
	for _, f := range Finders(model.ScreenDev) {
		if f.Screen != model.ScreenDev {
			t.Errorf("%s isn't on Dev", f.Group)
		}
		names = append(names, f.Group)
	}
	if got := strings.Join(names, ","); got != "Xcode,Simulators,Docker,Package caches,Projects" {
		t.Errorf("Dev finders = %s", got)
	}
	if len(Finders(model.ScreenJunk)) != len(JunkFinders()) {
		t.Error("Finders(junk) should be the Junk finders")
	}
}

func TestRunTurnsAPanicIntoThatGroupsError(t *testing.T) {
	finders := []Finder{
		{Group: "Broken", Screen: model.ScreenDev, Find: func(context.Context, Env) Result { panic("boom") }},
		{Group: "Fine", Screen: model.ScreenDev, Find: func(context.Context, Env) Result {
			return Result{Items: []model.CleanItem{{ID: "x", Group: "Fine"}}}
		}},
	}
	got := map[string]model.ScanUpdate{}
	for u := range Run(context.Background(), Env{}, finders, nil) {
		got[u.Finder] = u
	}
	if u := got["Broken"]; u.Err == nil || !strings.Contains(u.Err.Error(), "boom") {
		t.Errorf("Broken = %+v", u)
	}
	if u := got["Fine"]; len(u.Items) != 1 || u.Err != nil {
		t.Errorf("Fine = %+v", u)
	}
}

func TestRunCountsDoneInOrder(t *testing.T) {
	var finders []Finder
	for i := 0; i < 20; i++ {
		finders = append(finders, Finder{Group: "g", Screen: model.ScreenJunk, Find: func(context.Context, Env) Result { return Result{} }})
	}
	want := 1
	for u := range Run(context.Background(), Env{}, finders, nil) {
		if u.Done != want {
			t.Fatalf("Done = %d, want %d", u.Done, want)
		}
		want++
	}
}

func TestRunStreamsUpdatesThatAdd(t *testing.T) {
	finders := []Finder{
		{Group: "Copies", Screen: model.ScreenDupes, Stream: func(_ context.Context, _ Env, add func([]model.CleanItem, string)) Result {
			add([]model.CleanItem{{ID: "s1", Group: "Copies"}}, "Hashing 1 of 2 sets")
			add([]model.CleanItem{{ID: "s2", Group: "Copies", App: "Xcode"}}, "Hashing 2 of 2 sets")
			return Result{Notes: []model.Note{{Screen: model.ScreenDupes, Text: "1 file couldn't be read."}}}
		}},
	}
	var got []model.ScanUpdate
	for u := range Run(context.Background(), Env{}, finders, map[string]bool{"xcode": true}) {
		got = append(got, u)
	}
	if len(got) != 4 {
		t.Fatalf("updates = %+v", got)
	}
	if got[0].Add || len(got[0].Items) != 0 || got[0].Done != 0 {
		t.Errorf("first = %+v; it clears what an earlier scan found", got[0])
	}
	if !got[1].Add || got[1].Items[0].ID != "s1" || got[1].Status != "Hashing 1 of 2 sets" || got[1].Done != 0 || got[1].Total != 1 {
		t.Errorf("second = %+v", got[1])
	}
	if !got[2].Items[0].Busy {
		t.Error("streamed items are marked busy too")
	}
	if last := got[3]; !last.Add || last.Done != 1 || len(last.Notes) != 1 {
		t.Errorf("last = %+v; it adds the notes and counts the finder done", last)
	}
}

func TestRunStopsAStreamWhenNobodyReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	finders := []Finder{{Group: "Copies", Stream: func(ctx context.Context, _ Env, add func([]model.CleanItem, string)) Result {
		for i := 0; i < 1000; i++ {
			add(nil, "busy")
		}
		return Result{}
	}}}
	ch := Run(ctx, Env{}, finders, nil)
	<-ch
	cancel()
	for range ch { // must end: a stream blocked on a full channel gives up once ctx is cancelled
	}
}

func TestRunTurnsAStreamPanicIntoThatGroupsError(t *testing.T) {
	finders := []Finder{{Group: "Copies", Screen: model.ScreenDupes, Stream: func(_ context.Context, _ Env, add func([]model.CleanItem, string)) Result {
		add([]model.CleanItem{{ID: "a", Group: "Copies"}}, "")
		panic("boom")
	}}}
	var got []model.ScanUpdate
	for u := range Run(context.Background(), Env{}, finders, nil) {
		got = append(got, u)
	}
	last := got[len(got)-1]
	if last.Err == nil || !strings.Contains(last.Err.Error(), "boom") || !last.Add || last.Done != 1 {
		t.Errorf("last update = %+v", last)
	}
	added := false
	for _, u := range got {
		added = added || len(u.Items) == 1
	}
	if !added {
		t.Error("the set found before the panic was lost")
	}
}
