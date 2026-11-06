package actions

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
)

var (
	ctx = context.Background()
	t0  = time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
)

type fakeLookup map[int]model.Process

func (f fakeLookup) Lookup(_ context.Context, pid int) (model.Process, error) {
	p, ok := f[pid]
	if !ok {
		return model.Process{}, model.ErrNoProcess
	}
	return p, nil
}

type fakeSignals struct {
	sent      []syscall.Signal
	pids      []int
	exitAfter int // liveness checks until the process is gone; -1 means never
	checks    int
	err       error // returned for the real signal
}

func (f *fakeSignals) signal(pid int, sig syscall.Signal) error {
	if sig == 0 {
		f.checks++
		if f.exitAfter >= 0 && f.checks >= f.exitAfter {
			return syscall.ESRCH
		}
		return nil
	}
	f.pids = append(f.pids, pid)
	f.sent = append(f.sent, sig)
	return f.err
}

func newService(sig *fakeSignals, procs fakeLookup) (*Service, *int) {
	sleeps := new(int)
	return &Service{Procs: procs, Signal: sig.signal, Sleep: func(time.Duration) { *sleeps++ }, Self: 99999}, sleeps
}

func node() model.Process {
	return model.Process{ProcessID: model.ProcessID{PID: 48121, Name: "node", StartTime: t0}, UID: 501}
}

func TestVerify(t *testing.T) {
	svc, _ := newService(&fakeSignals{}, fakeLookup{48121: node()})
	if _, err := svc.Verify(ctx, node().ProcessID); err != nil {
		t.Fatalf("Verify(node) = %v", err)
	}
	changed := node().ProcessID
	changed.StartTime = t0.Add(time.Minute)
	if _, err := svc.Verify(ctx, changed); !errors.Is(err, ErrChanged) {
		t.Errorf("reused PID: got %v, want ErrChanged", err)
	}
	if _, err := svc.Verify(ctx, model.ProcessID{PID: 5555, Name: "gone", StartTime: t0}); !errors.Is(err, ErrAlreadyExited) {
		t.Errorf("gone: got %v, want ErrAlreadyExited", err)
	}
	var pe *ProtectedError
	if _, err := svc.Verify(ctx, model.ProcessID{PID: 400, Name: "WindowServer"}); !errors.As(err, &pe) {
		t.Errorf("WindowServer: got %v, want *ProtectedError", err)
	}
	if _, err := svc.Verify(ctx, model.ProcessID{PID: 99999, Name: "soap"}); !errors.As(err, &pe) {
		t.Errorf("self: got %v, want *ProtectedError", err)
	}
}

func TestRefusal(t *testing.T) {
	svc, _ := newService(&fakeSignals{}, nil)
	if err := svc.Refusal(model.ProcessID{PID: 1, Name: "launchd"}); err == nil {
		t.Error("launchd should be refused")
	}
	if err := svc.Refusal(node().ProcessID); err != nil {
		t.Errorf("node refused: %v", err)
	}
}

func TestTerminateWaitsForExit(t *testing.T) {
	sig := &fakeSignals{exitAfter: 2}
	svc, sleeps := newService(sig, nil)
	out, err := svc.Terminate(48121, false)
	if err != nil || out != Exited {
		t.Fatalf("Terminate = %v, %v", out, err)
	}
	if !slices.Equal(sig.sent, []syscall.Signal{syscall.SIGTERM}) || sig.pids[0] != 48121 {
		t.Errorf("sent %v to %v", sig.sent, sig.pids)
	}
	if *sleeps != 2 {
		t.Errorf("slept %d times, want 2", *sleeps)
	}
}

func TestTerminateGivesUpAfterThreeSeconds(t *testing.T) {
	sig := &fakeSignals{exitAfter: -1}
	svc, sleeps := newService(sig, nil)
	out, err := svc.Terminate(48121, false)
	if err != nil || out != StillRunning {
		t.Fatalf("Terminate = %v, %v", out, err)
	}
	if *sleeps != 15 {
		t.Errorf("slept %d times, want 15 (3 seconds in 200 ms steps)", *sleeps)
	}
}

func TestTerminateForceSendsSIGKILL(t *testing.T) {
	sig := &fakeSignals{exitAfter: 1}
	svc, _ := newService(sig, nil)
	if _, err := svc.Terminate(48121, true); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sig.sent, []syscall.Signal{syscall.SIGKILL}) {
		t.Errorf("sent %v, want SIGKILL", sig.sent)
	}
}

func TestTerminateOtherUsersProcess(t *testing.T) {
	svc, _ := newService(&fakeSignals{err: syscall.EPERM}, nil)
	if _, err := svc.Terminate(901, false); !errors.Is(err, ErrNeedsSudo) {
		t.Fatalf("got %v, want ErrNeedsSudo", err)
	}
}

func TestTerminateAlreadyGone(t *testing.T) {
	svc, _ := newService(&fakeSignals{err: syscall.ESRCH}, nil)
	out, err := svc.Terminate(48121, false)
	if err != nil || out != Exited {
		t.Fatalf("Terminate = %v, %v", out, err)
	}
}

func TestSudoKill(t *testing.T) {
	svc := &Service{}
	cmd, err := svc.SudoKill(42, false)
	if err != nil || !slices.Equal(cmd.Args, []string{"sudo", "kill", "-TERM", "42"}) {
		t.Errorf("SudoKill(42) = %v, %v", cmd, err)
	}
	cmd, err = svc.SudoKill(42, true)
	if err != nil || !slices.Equal(cmd.Args, []string{"sudo", "kill", "-KILL", "42"}) {
		t.Errorf("SudoKill(42, force) = %v, %v", cmd, err)
	}
}

func TestSignalsRefuseSpecialPIDs(t *testing.T) {
	sig := &fakeSignals{}
	svc, _ := newService(sig, nil)
	for _, pid := range []int{1, 0, -1} {
		if _, err := svc.Terminate(pid, true); err == nil {
			t.Errorf("Terminate(%d) should be refused", pid)
		}
		if _, err := svc.SudoKill(pid, true); err == nil {
			t.Errorf("SudoKill(%d) should be refused", pid)
		}
	}
	if len(sig.sent) != 0 {
		t.Errorf("signals were sent: %v", sig.sent)
	}
}

const chromePath = "/Applications/Google Chrome.app"

// quitRunner fakes plutil and osascript. It records every command that ran.
type quitRunner struct {
	calls       [][]string
	plutilOut   string
	plutilErr   error
	osascript   string
	osascriptEr error
}

func (q *quitRunner) run(_ context.Context, name string, args ...string) ([]byte, error) {
	q.calls = append(q.calls, append([]string{name}, args...))
	if name == "plutil" {
		return []byte(q.plutilOut), q.plutilErr
	}
	return []byte(q.osascript), q.osascriptEr
}

func newQuitRunner() *quitRunner {
	return &quitRunner{plutilOut: "com.google.Chrome\n", osascript: "asked\n"}
}

func TestQuitApp(t *testing.T) {
	q := newQuitRunner()
	svc := &Service{Run: q.run}
	if err := svc.QuitApp(ctx, chromePath); err != nil {
		t.Fatal(err)
	}
	wantPlutil := []string{"plutil", "-extract", "CFBundleIdentifier", "raw", "-o", "-", chromePath + "/Contents/Info.plist"}
	wantScript := []string{
		"osascript",
		"-e", `if application id "com.google.Chrome" is running then`,
		"-e", `tell application id "com.google.Chrome" to quit`,
		"-e", `return "asked"`,
		"-e", `end if`,
		"-e", `return "not running"`,
	}
	if len(q.calls) != 2 || !slices.Equal(q.calls[0], wantPlutil) || !slices.Equal(q.calls[1], wantScript) {
		t.Errorf("ran %q", q.calls)
	}
}

func TestQuitAppNotRunning(t *testing.T) {
	q := newQuitRunner()
	q.osascript = "not running\n"
	svc := &Service{Run: q.run}
	if err := svc.QuitApp(ctx, chromePath); !errors.Is(err, ErrNotRegularApp) {
		t.Fatalf("got %v, want ErrNotRegularApp", err)
	}
}

func TestQuitAppRefusesWhenBundleIDIsUnusable(t *testing.T) {
	cases := map[string]*quitRunner{
		"plutil fails":  {plutilErr: errors.New("exit status 1")},
		"quote in ID":   {plutilOut: `com.x" & (do shell script "id") & "`},
		"empty ID":      {plutilOut: "\n"},
		"space in ID":   {plutilOut: "com.x y"},
		"newline in ID": {plutilOut: "com.x\ncom.y"},
	}
	for name, q := range cases {
		svc := &Service{Run: q.run}
		if err := svc.QuitApp(ctx, chromePath); !errors.Is(err, ErrNotRegularApp) {
			t.Errorf("%s: got %v, want ErrNotRegularApp", name, err)
		}
		for _, c := range q.calls {
			if c[0] == "osascript" {
				t.Errorf("%s: osascript ran: %q", name, c)
			}
		}
	}
}

func TestQuitAppErrors(t *testing.T) {
	cases := []struct {
		err  string
		want error
	}{
		{"osascript: exit status 1: execution error: Not authorized to send Apple events to Slack. (-1743)", ErrAutomationDenied},
		{"osascript: exit status 1: execution error: User canceled. (-128)", ErrQuitCancelled},
	}
	for _, c := range cases {
		q := newQuitRunner()
		q.osascriptEr = errors.New(c.err)
		svc := &Service{Run: q.run}
		if err := svc.QuitApp(ctx, chromePath); !errors.Is(err, c.want) {
			t.Errorf("%q: got %v, want %v", c.err, err, c.want)
		}
	}
	q := newQuitRunner()
	q.osascriptEr = errors.New("boom")
	if err := (&Service{Run: q.run}).QuitApp(ctx, chromePath); err == nil || err.Error() != "boom" {
		t.Errorf("other errors pass through, got %v", err)
	}
}

func TestStopContainer(t *testing.T) {
	var got []string
	svc := &Service{DockerRun: func(_ context.Context, name string, args ...string) ([]byte, error) {
		got = append([]string{name}, args...)
		return nil, nil
	}}
	if err := svc.StopContainer(ctx, "store-backend-mysql-1", false); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"docker", "stop", "store-backend-mysql-1"}) {
		t.Errorf("ran %v", got)
	}
	if err := svc.StopContainer(ctx, "web-1", true); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"docker", "kill", "web-1"}) {
		t.Errorf("force ran %v", got)
	}
}

func TestTerminateRealProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("starts a real process")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go cmd.Wait() // reap the child so it doesn't linger as a zombie
	svc := &Service{Signal: syscall.Kill, Sleep: time.Sleep}
	out, err := svc.Terminate(cmd.Process.Pid, false)
	if err != nil || out != Exited {
		t.Fatalf("Terminate = %v, %v", out, err)
	}
}
