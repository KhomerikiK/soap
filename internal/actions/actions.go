// Package actions is the only part of soap that changes system state: it signals processes,
// quits apps and stops containers, always after checking the target again.
package actions

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
)

// Runner runs a command and returns its standard output. sys.Runner values can be assigned to it.
type Runner = func(ctx context.Context, name string, args ...string) ([]byte, error)

// ProcessLookup finds a running process by PID. It returns model.ErrNoProcess when there's none.
type ProcessLookup interface {
	Lookup(ctx context.Context, pid int) (model.Process, error)
}

// Outcome is what happened after a signal was sent.
type Outcome int

const (
	Exited Outcome = iota
	StillRunning
)

var (
	ErrAlreadyExited    = errors.New("already exited")
	ErrChanged          = errors.New("that process changed since it was shown, so nothing was stopped")
	ErrNeedsSudo        = errors.New("needs admin rights")
	ErrAutomationDenied = errors.New("macOS blocked the quit request")
	ErrQuitCancelled    = errors.New("the quit was cancelled")
	ErrNotRegularApp    = errors.New("not running as a regular app")
)

var bundleID = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)

// ProtectedError means soap refuses to stop a process.
type ProtectedError struct {
	Name, Reason string
}

func (e *ProtectedError) Error() string {
	return fmt.Sprintf("soap won't stop %s: %s", e.Name, e.Reason)
}

const (
	waitStep  = 200 * time.Millisecond
	waitSteps = 15 // 3 seconds in total
)

// Service carries out every action that changes system state.
type Service struct {
	Procs     ProcessLookup
	Signal    func(pid int, sig syscall.Signal) error // syscall.Kill in production
	Sleep     func(time.Duration)
	Run       Runner // used for plutil and osascript when quitting an app
	DockerRun Runner // used for docker, with a longer timeout
	Self      int    // soap's own PID
}

// Refusal returns a *ProtectedError when target is on the protected list, so callers can refuse
// before asking for confirmation.
func (s *Service) Refusal(target model.ProcessID) error {
	if reason, ok := model.Protected(target, s.Self); ok {
		return &ProtectedError{Name: target.Name, Reason: reason}
	}
	return nil
}

// Verify checks that target isn't protected and is still the process the user saw.
func (s *Service) Verify(ctx context.Context, target model.ProcessID) (model.Process, error) {
	if err := s.Refusal(target); err != nil {
		return model.Process{}, err
	}
	cur, err := s.Procs.Lookup(ctx, target.PID)
	if errors.Is(err, model.ErrNoProcess) {
		return model.Process{}, ErrAlreadyExited
	}
	if err != nil {
		return model.Process{}, err
	}
	if !model.SameProcess(target, cur.ProcessID) {
		return model.Process{}, ErrChanged
	}
	return cur, nil
}

// Terminate sends SIGTERM, or SIGKILL when force is set, then waits up to 3 seconds for the
// process to exit. It returns ErrNeedsSudo when the process belongs to another user.
func (s *Service) Terminate(pid int, force bool) (Outcome, error) {
	if err := checkPID(pid); err != nil {
		return 0, err
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	if err := s.Signal(pid, sig); err != nil {
		switch {
		case errors.Is(err, syscall.EPERM):
			return 0, ErrNeedsSudo
		case errors.Is(err, syscall.ESRCH):
			return Exited, nil
		}
		return 0, err
	}
	for range waitSteps {
		s.Sleep(waitStep)
		if err := s.Signal(pid, 0); errors.Is(err, syscall.ESRCH) {
			return Exited, nil
		}
	}
	return StillRunning, nil
}

// SudoKill returns a command that kills pid through sudo. The caller attaches it to the
// terminal, so sudo asks for the password itself.
func (s *Service) SudoKill(pid int, force bool) (*exec.Cmd, error) {
	if err := checkPID(pid); err != nil {
		return nil, err
	}
	sig := "-TERM"
	if force {
		sig = "-KILL"
	}
	return exec.Command("sudo", "kill", sig, strconv.Itoa(pid)), nil
}

// checkPID refuses PIDs that kill(2) treats specially: 0 and negative numbers signal whole
// process groups, and 1 is launchd. Callers verify targets first; this is a second line of
// defence.
func checkPID(pid int) error {
	if pid <= 1 {
		return fmt.Errorf("soap won't signal PID %d", pid)
	}
	return nil
}

// QuitApp asks the running app at appPath to quit, the same way ⌘Q does. It addresses the app
// by bundle ID, read from its Info.plist, so a different app with the same display name can't
// be hit. The "is running" check stops AppleScript from launching an app that isn't open. It
// returns ErrNotRegularApp when the app has no usable bundle ID or isn't running as an app.
func (s *Service) QuitApp(ctx context.Context, appPath string) error {
	out, err := s.Run(ctx, "plutil", "-extract", "CFBundleIdentifier", "raw", "-o", "-", appPath+"/Contents/Info.plist")
	id := strings.TrimSpace(string(out))
	if err != nil || !bundleID.MatchString(id) {
		return ErrNotRegularApp
	}
	out, err = s.Run(ctx, "osascript",
		"-e", fmt.Sprintf(`if application id "%s" is running then`, id),
		"-e", fmt.Sprintf(`tell application id "%s" to quit`, id),
		"-e", `return "asked"`,
		"-e", `end if`,
		"-e", `return "not running"`,
	)
	switch {
	case err != nil && strings.Contains(err.Error(), "-1743"):
		return ErrAutomationDenied
	case err != nil && strings.Contains(err.Error(), "-128"):
		return ErrQuitCancelled
	case err != nil:
		return err
	case strings.TrimSpace(string(out)) == "not running":
		return ErrNotRegularApp
	}
	return nil
}

// StopContainer stops a Docker container, or kills it when force is set.
func (s *Service) StopContainer(ctx context.Context, name string, force bool) error {
	verb := "stop"
	if force {
		verb = "kill"
	}
	_, err := s.DockerRun(ctx, "docker", verb, name)
	return err
}
