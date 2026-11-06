package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/KhomerikiK/soap/internal/actions"
	"github.com/KhomerikiK/soap/internal/model"
)

func TestPortsScreenShowsListeners(t *testing.T) {
	h := openPorts(t, portsSnapshot())
	s := h.screen()
	for _, want := range []string{"1 Ports", "Listening ports", "sort: port", "3000", "node", "next dev", "→ store-backend-mysql-1", "php-fpm", "mem 11.0 GB/18.0 GB warning"} {
		if !strings.Contains(s, want) {
			t.Errorf("screen is missing %q:\n%s", want, s)
		}
	}
}

func TestKillOwnProcess(t *testing.T) {
	h := openPorts(t, portsSnapshot())
	h.press("x")
	if s := h.screen(); !strings.Contains(s, "Kill node (pid 48121) on :3000?") {
		t.Fatalf("no confirmation:\n%s", s)
	}
	cmd := h.press("y")
	if !strings.Contains(h.screen(), "Stopping node (pid 48121) on :3000…") {
		t.Errorf("no working text before the kill finishes:\n%s", h.screen())
	}
	h.run(cmd)
	if !slices.Equal(h.act.terminated, []termCall{{48121, false}}) {
		t.Errorf("terminated %v", h.act.terminated)
	}
	if s := h.screen(); !strings.Contains(s, "Stopped node (pid 48121) on :3000.") {
		t.Errorf("no result:\n%s", s)
	}
}

func TestKillCancelled(t *testing.T) {
	h := openPorts(t, portsSnapshot())
	if cmd := h.press("x", "n"); cmd != nil {
		t.Error("cancelling shouldn't run anything")
	}
	if len(h.act.terminated) != 0 {
		t.Errorf("terminated %v", h.act.terminated)
	}
	if !strings.Contains(h.screen(), "Cancelled.") {
		t.Error("no cancelled message")
	}
}

func TestKillOtherUsersProcessUsesSudo(t *testing.T) {
	h := openPorts(t, portsSnapshot())
	h.act.verifyUID = map[int]int{901: 0}
	h.press("j", "j", "x")
	if s := h.screen(); !strings.Contains(s, "It runs as root, so sudo will ask for your password.") {
		t.Fatalf("no sudo warning:\n%s", s)
	}
	cmd := h.run(h.press("y"))
	if cmd == nil || !slices.Equal(h.act.sudo, []int{901}) {
		t.Errorf("sudo calls = %v, cmd = %v", h.act.sudo, cmd)
	}
	if len(h.act.terminated) != 0 {
		t.Error("other users' processes go through sudo, not a direct signal")
	}
}

func TestDockerPortStopsContainer(t *testing.T) {
	h := openPorts(t, portsSnapshot())
	h.press("j", "x")
	if s := h.screen(); !strings.Contains(s, ":3306 is forwarded by Docker to store-backend-mysql-1. Stop that container?") {
		t.Fatalf("no container confirmation:\n%s", s)
	}
	cmd := h.press("y")
	if !strings.Contains(h.screen(), "Stopping container store-backend-mysql-1…") {
		t.Errorf("no working text:\n%s", h.screen())
	}
	h.run(cmd)
	if !slices.Equal(h.act.stopped, []string{"store-backend-mysql-1"}) || len(h.act.terminated) != 0 {
		t.Errorf("stopped %v, terminated %v", h.act.stopped, h.act.terminated)
	}
	if !strings.Contains(h.screen(), "Stopped container store-backend-mysql-1. Freed :3306.") {
		t.Error("no result message")
	}
}

func TestStillRunningOffersForceKill(t *testing.T) {
	h := openPorts(t, portsSnapshot())
	h.act.outcome = actions.StillRunning
	h.run(h.press("x", "y"))
	if s := h.screen(); !strings.Contains(s, "still running after 3 seconds. Force kill it?") {
		t.Fatalf("no force offer:\n%s", s)
	}
	h.act.outcome = actions.Exited
	cmd := h.press("y")
	if !strings.Contains(h.screen(), "Killing node (pid 48121) on :3000…") {
		t.Errorf("no working text for the force kill:\n%s", h.screen())
	}
	h.run(cmd)
	if !slices.Equal(h.act.terminated, []termCall{{48121, false}, {48121, true}}) {
		t.Errorf("terminated %v", h.act.terminated)
	}
}

func TestProtectedProcessIsRefusedWithoutAsking(t *testing.T) {
	snap := portsSnapshot()
	snap.Listeners = []model.Listener{{Port: 7000, Proto: "tcp46", PID: 1, Process: "launchd", User: "root", UID: 0, ID: model.ProcessID{PID: 1, Name: "launchd", StartTime: t0}}}
	h := openPorts(t, snap)
	if cmd := h.press("x"); cmd != nil {
		t.Error("a refusal shouldn't run anything")
	}
	if s := h.screen(); !strings.Contains(s, "soap won't stop launchd") || strings.Contains(s, "[y]") {
		t.Errorf("want a refusal without a confirmation:\n%s", s)
	}
}

func TestChangedProcessIsNotKilled(t *testing.T) {
	h := openPorts(t, portsSnapshot())
	h.act.verifyErr = actions.ErrChanged
	h.run(h.press("x", "y"))
	if len(h.act.terminated) != 0 {
		t.Errorf("terminated %v", h.act.terminated)
	}
	if !strings.Contains(h.screen(), "That process changed since it was shown. Nothing was stopped.") {
		t.Error("no changed-process message")
	}
}

func TestFilterRows(t *testing.T) {
	h := openPorts(t, portsSnapshot())
	h.press("/", "p", "h", "p", "enter")
	s := h.screen()
	if !strings.Contains(s, "php-fpm") || strings.Contains(s, "next dev") {
		t.Errorf("filter php should leave only php-fpm:\n%s", s)
	}
	h.press("esc")
	if !strings.Contains(h.screen(), "next dev") {
		t.Error("esc should clear the filter")
	}
}

func TestSortCycles(t *testing.T) {
	h := openPorts(t, portsSnapshot())
	h.press("s")
	s := h.screen()
	if !strings.Contains(s, "sort: process") {
		t.Fatalf("sort didn't change:\n%s", s)
	}
	if strings.Index(s, "com.docker.backend") > strings.Index(s, "next dev") {
		t.Error("com.docker.backend should sort before node")
	}
}

func TestPauseAndResume(t *testing.T) {
	h := openPorts(t, portsSnapshot())
	h.press("p")
	if !strings.Contains(h.screen(), "paused") {
		t.Fatal("status bar should say paused")
	}
	cmd := h.press("p")
	if strings.Contains(h.screen(), "paused") {
		t.Error("still paused after resuming")
	}
	before := len(h.opts)
	h.run(cmd)
	if len(h.opts) != before+1 {
		t.Error("resuming should refresh at once")
	}
}

func TestHelpOverlay(t *testing.T) {
	h := openPorts(t, portsSnapshot())
	h.press("?")
	if !strings.Contains(h.screen(), "Press any key to close this help.") {
		t.Fatal("no help")
	}
	h.press("j")
	if strings.Contains(h.screen(), "Press any key") {
		t.Error("any key should close the help")
	}
}

func TestSmallWindow(t *testing.T) {
	h := openPorts(t, portsSnapshot())
	h.send(tea.WindowSizeMsg{Width: 60, Height: 15})
	if s := h.screen(); !strings.Contains(s, "Make the window at least 80×20. It's 60×15 now.") {
		t.Errorf("got:\n%s", s)
	}
}

func TestSocketErrorKeepsTheFrame(t *testing.T) {
	snap := portsSnapshot()
	snap.Listeners, snap.SocketErr = nil, errors.New("unexpected netstat output: no header line")
	h := openPorts(t, snap)
	s := h.screen()
	for _, want := range []string{"Couldn't read sockets: unexpected netstat output", "macOS 26.5", "mem 11.0 GB"} {
		if !strings.Contains(s, want) {
			t.Errorf("screen is missing %q:\n%s", want, s)
		}
	}
}

func TestRefreshLoop(t *testing.T) {
	h := newHarness(t, portsSnapshot(), func(d *Deps) { d.Interval = time.Millisecond })
	first := h.send(tickMsg{})
	batch, ok := first().(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatalf("a tick should start a collect and schedule the next tick, got %T", first())
	}
	if msg := h.send(tickMsg{})(); msg != (tickMsg{}) {
		t.Errorf("a tick during a collect should only schedule the next tick, got %T", msg)
	}
	h.send(batch[0]()) // the collect finishes
	h.press("p")
	if msg := h.send(tickMsg{})(); msg != (tickMsg{}) {
		t.Errorf("a tick while paused should only schedule the next tick, got %T", msg)
	}
}

func TestRefreshAskedForDuringACollectRunsAfterIt(t *testing.T) {
	h := openPorts(t, portsSnapshot())
	first := h.press("p", "p") // resuming starts a collect
	if first == nil {
		t.Fatal("resuming should refresh")
	}
	if cmd := h.send(resultMsg{text: "Stopped node."}); cmd != nil {
		t.Error("a second collect shouldn't start while one is running")
	}
	next := h.send(first()) // the first collect finishes
	if next == nil {
		t.Fatal("the refresh asked for during the collect should start now")
	}
	before := len(h.opts)
	h.run(next)
	if len(h.opts) != before+1 {
		t.Error("the queued refresh didn't collect")
	}
}

func TestMultiLineErrorsStayOnOneLine(t *testing.T) {
	snap := portsSnapshot()
	snap.DockerErr = errors.New("exit 1:\nno daemon")
	h := openPorts(t, snap)
	h.send(resultMsg{text: "Couldn't stop node: first line\nsecond line", err: true})
	s := h.screen()
	if n := strings.Count(s, "\n") + 1; n != 30 {
		t.Errorf("screen has %d lines, want 30:\n%s", n, s)
	}
	if !strings.Contains(s, "names are missing: exit 1: no daemon") || !strings.Contains(s, "first line second line") {
		t.Errorf("errors should be joined onto one line:\n%s", s)
	}
}

func TestDockerErrorIsShown(t *testing.T) {
	snap := portsSnapshot()
	snap.DockerErr = errors.New("docker timed out after 15s")
	if s := openPorts(t, snap).screen(); !strings.Contains(s, "Couldn't read Docker containers, so container names are missing: docker timed out after 15s") {
		t.Errorf("no Docker error:\n%s", s)
	}
}

func TestWidthsAndFit(t *testing.T) {
	if got := widths([]column{{"A", 6}, {"B", 6}, {"C", 0}}, 80); !slices.Equal(got, []int{6, 6, 65}) {
		t.Errorf("widths = %v", got)
	}
	if got := fit("hello", 3); got != "he…" {
		t.Errorf("fit cut = %q", got)
	}
	if got := fit("hi", 4); got != "hi  " {
		t.Errorf("fit pad = %q", got)
	}
}

func TestEngineRowWithoutContainerIsNotKilled(t *testing.T) {
	snap := portsSnapshot()
	snap.Listeners[1].Container = ""
	snap.Listeners[1].Engine = true
	h := openPorts(t, snap)
	for _, key := range []string{"x", "X"} {
		if cmd := h.press("j", key); cmd != nil {
			t.Errorf("%s: a refusal shouldn't run anything", key)
		}
		want := "Docker owns :3306 but no container is known for it. Nothing was stopped."
		if s := h.screen(); !strings.Contains(s, want) || strings.Contains(s, "[y]") {
			t.Errorf("%s: no refusal, or it asked anyway:\n%s", key, s)
		}
		h.press("k")
	}
	if len(h.act.terminated) != 0 || len(h.act.stopped) != 0 {
		t.Errorf("terminated %v, stopped %v", h.act.terminated, h.act.stopped)
	}
}

func TestSudoResultSaysTheSignalWasSent(t *testing.T) {
	if got := sudoResult("php-fpm (pid 901)", false, nil); got != (resultMsg{text: "Sent SIGTERM to php-fpm (pid 901) with sudo."}) {
		t.Errorf("got %+v", got)
	}
	if got := sudoResult("php-fpm (pid 901)", true, nil); got != (resultMsg{text: "Sent SIGKILL to php-fpm (pid 901) with sudo."}) {
		t.Errorf("got %+v", got)
	}
	if got := sudoResult("php-fpm (pid 901)", false, errors.New("exit 1")); got != (resultMsg{text: "Not killed: sudo was cancelled or failed.", err: true}) {
		t.Errorf("got %+v", got)
	}
}
