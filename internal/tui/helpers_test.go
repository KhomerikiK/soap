package tui

import (
	"context"
	"os/exec"
	"testing"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/KhomerikiK/soap/internal/actions"
	"github.com/KhomerikiK/soap/internal/engine"
	"github.com/KhomerikiK/soap/internal/model"
)

const me = 501

var t0 = time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)

type termCall struct {
	pid   int
	force bool
}

// fakeActions records what the TUI asked for instead of touching real processes.
type fakeActions struct {
	verifyErr  error
	verifyUID  map[int]int // the UID Verify reports per PID; me when missing
	outcome    actions.Outcome
	termErr    error
	quitErr    error
	terminated []termCall
	sudo       []int
	stopped    []string
	quit       []string
}

func (f *fakeActions) Refusal(t model.ProcessID) error {
	if reason, ok := model.Protected(t, 99999); ok {
		return &actions.ProtectedError{Name: t.Name, Reason: reason}
	}
	return nil
}

func (f *fakeActions) Verify(_ context.Context, t model.ProcessID) (model.Process, error) {
	if err := f.Refusal(t); err != nil {
		return model.Process{}, err
	}
	if f.verifyErr != nil {
		return model.Process{}, f.verifyErr
	}
	uid, ok := f.verifyUID[t.PID]
	if !ok {
		uid = me
	}
	return model.Process{ProcessID: t, UID: uid}, nil
}

func (f *fakeActions) Terminate(pid int, force bool) (actions.Outcome, error) {
	f.terminated = append(f.terminated, termCall{pid, force})
	return f.outcome, f.termErr
}

func (f *fakeActions) SudoKill(pid int, _ bool) (*exec.Cmd, error) {
	f.sudo = append(f.sudo, pid)
	return exec.Command("true"), nil
}

func (f *fakeActions) StopContainer(_ context.Context, name string, _ bool) error {
	f.stopped = append(f.stopped, name)
	return nil
}

func (f *fakeActions) QuitApp(_ context.Context, appPath string) error {
	f.quit = append(f.quit, appPath)
	return f.quitErr
}

// harness drives a Model the way Bubble Tea would, one message at a time.
type harness struct {
	t    *testing.T
	m    Model
	act  *fakeActions
	snap engine.Snapshot
	opts []engine.Options // the options of every Collect call
}

func newHarness(t *testing.T, snap engine.Snapshot, opts ...func(*Deps)) *harness {
	t.Helper()
	h := &harness{t: t, act: &fakeActions{}, snap: snap}
	deps := Deps{
		Collect: func(_ context.Context, opt engine.Options) engine.Snapshot {
			h.opts = append(h.opts, opt)
			return h.snap
		},
		Actions: h.act,
		UID:     me,
		MacOS:   "26.5",
	}
	for _, o := range opts {
		o(&deps)
	}
	h.m = New(deps)
	h.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	h.send(snapshotMsg{snap: snap})
	return h
}

// send delivers msg and returns the command the model asked for.
func (h *harness) send(msg tea.Msg) tea.Cmd {
	h.t.Helper()
	next, cmd := h.m.Update(msg)
	h.m = next.(Model)
	return cmd
}

// press sends keys in order and returns the command from the last one.
func (h *harness) press(keys ...string) tea.Cmd {
	h.t.Helper()
	var cmd tea.Cmd
	for _, k := range keys {
		cmd = h.send(keyMsg(k))
	}
	return cmd
}

// run executes cmd, as Bubble Tea would in the background, and delivers its message.
func (h *harness) run(cmd tea.Cmd) tea.Cmd {
	h.t.Helper()
	if cmd == nil {
		h.t.Fatal("expected a command, got nil")
	}
	return h.send(cmd())
}

// screen is what's on screen, without colors.
func (h *harness) screen() string { return ansi.Strip(h.m.View().Content) }

func keyMsg(k string) tea.KeyPressMsg {
	switch k {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	}
	r := []rune(k)[0]
	if unicode.IsUpper(r) {
		return tea.KeyPressMsg{Code: unicode.ToLower(r), Text: k, Mod: tea.ModShift}
	}
	return tea.KeyPressMsg{Code: r, Text: k}
}

func portsSnapshot() engine.Snapshot {
	id := func(pid int, name string) model.ProcessID {
		return model.ProcessID{PID: pid, Name: name, StartTime: t0}
	}
	return engine.Snapshot{
		Memory: model.MemoryStats{Total: 18 << 30, Used: 11 << 30, Pressure: model.PressureWarning},
		Listeners: []model.Listener{
			{Port: 3000, Proto: "tcp4", PID: 48121, Process: "node", User: "me", UID: me, Bind: "127.0.0.1", Note: "next dev", ID: id(48121, "node")},
			{Port: 3306, Proto: "tcp46", PID: 912, Process: "com.docker.backend", User: "me", UID: me, Bind: "*", Container: "store-backend-mysql-1", ID: id(912, "com.docker.backend")},
			{Port: 9000, Proto: "tcp4", PID: 901, Process: "php-fpm", User: "root", UID: 0, Bind: "127.0.0.1", ID: id(901, "php-fpm")},
		},
	}
}

// openPorts is a harness on the Ports tab. soap opens on Home, so Ports' tests press 1 first.
func openPorts(t *testing.T, snap engine.Snapshot, opts ...func(*Deps)) *harness {
	t.Helper()
	h := newHarness(t, snap, opts...)
	h.press("1")
	return h
}
