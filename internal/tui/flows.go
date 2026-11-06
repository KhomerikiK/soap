package tui

import (
	"context"
	"errors"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/KhomerikiK/soap/internal/actions"
	"github.com/KhomerikiK/soap/internal/model"
)

type (
	// sudoMsg asks the TUI to hand the terminal to sudo.
	sudoMsg struct {
		pid   int
		force bool
		label string
	}
	// stillRunningMsg means SIGTERM didn't stop the process within 3 seconds.
	stillRunningMsg struct {
		target model.ProcessID
		label  string
	}
	// automationDeniedMsg means macOS refused to let soap quit an app.
	automationDeniedMsg struct {
		target model.ProcessID
		label  string
	}
)

func (m Model) handleFlow(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case sudoMsg:
		label := msg.label
		cmd, err := m.deps.Actions.SudoKill(msg.pid, msg.force)
		if err != nil {
			return m, func() tea.Msg { return actionError(label, err) }
		}
		force := msg.force
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return sudoResult(label, force, err) })
	case stillRunningMsg:
		m.confirm = &confirmation{
			text:    fmt.Sprintf("%s is still running after 3 seconds. Force kill it?", msg.label),
			danger:  true,
			yes:     m.killCmd(msg.target, true, msg.label),
			working: fmt.Sprintf("Killing %s…", msg.label),
		}
	case automationDeniedMsg:
		m.confirm = &confirmation{
			text:    fmt.Sprintf("macOS blocked the quit request for %s. Send SIGTERM instead?", msg.label),
			detail:  "To allow quitting apps, see System Settings › Privacy & Security › Automation.",
			yes:     m.killCmd(msg.target, false, msg.label),
			working: fmt.Sprintf("Stopping %s…", msg.label),
		}
	}
	return m, nil
}

// sudoResult is the message line after sudo ran. A zero exit only means the signal was delivered,
// so it says "sent", not "stopped".
func sudoResult(label string, force bool, err error) tea.Msg {
	if err != nil {
		return resultMsg{text: "Not killed: sudo was cancelled or failed.", err: true}
	}
	sig := "SIGTERM"
	if force {
		sig = "SIGKILL"
	}
	return resultMsg{text: fmt.Sprintf("Sent %s to %s with sudo.", sig, label)}
}

// actionError turns an action's error into a result for the message line.
func actionError(label string, err error) tea.Msg {
	var pe *actions.ProtectedError
	switch {
	case errors.Is(err, actions.ErrAlreadyExited):
		return resultMsg{text: fmt.Sprintf("%s already exited.", label)}
	case errors.Is(err, actions.ErrChanged):
		return resultMsg{text: "That process changed since it was shown. Nothing was stopped.", err: true}
	case errors.As(err, &pe):
		return resultMsg{text: pe.Error(), err: true}
	}
	return resultMsg{text: fmt.Sprintf("Couldn't stop %s: %v", label, err), err: true}
}

// killCmd checks target again, then signals it, or hands over to sudo when it belongs to
// another user.
func (m Model) killCmd(target model.ProcessID, force bool, label string) tea.Cmd {
	act, me := m.deps.Actions, m.deps.UID
	return func() tea.Msg {
		cur, err := act.Verify(context.Background(), target)
		if err != nil {
			return actionError(label, err)
		}
		if cur.UID != me {
			return sudoMsg{pid: target.PID, force: force, label: label}
		}
		outcome, err := act.Terminate(target.PID, force)
		switch {
		case errors.Is(err, actions.ErrNeedsSudo):
			return sudoMsg{pid: target.PID, force: force, label: label}
		case err != nil:
			return actionError(label, err)
		case outcome == actions.StillRunning:
			return stillRunningMsg{target: target, label: label}
		}
		return resultMsg{text: fmt.Sprintf("Stopped %s.", label)}
	}
}

// askKill asks before signalling target. Protected processes are refused without asking. owner
// names the user that owns the process, or "" when it isn't known.
func (m *Model) askKill(target model.ProcessID, uid int, force bool, label, owner string) {
	if err := m.deps.Actions.Refusal(target); err != nil {
		m.setMsg(err.Error(), warn)
		return
	}
	verb := "Kill"
	if force {
		verb = "Force kill"
	}
	working := fmt.Sprintf("Stopping %s…", label)
	if force {
		working = fmt.Sprintf("Killing %s…", label)
	}
	c := &confirmation{text: fmt.Sprintf("%s %s?", verb, label), danger: force, yes: m.killCmd(target, force, label), working: working}
	if uid != m.deps.UID {
		if owner == "" {
			owner = "another user"
		}
		c.detail = fmt.Sprintf("It runs as %s, so sudo will ask for your password.", owner)
	}
	m.confirm = c
}

// askContainer asks before stopping the container behind a Docker-forwarded port.
func (m *Model) askContainer(name string, port uint16, force bool) {
	act := m.deps.Actions
	verb, done := "Stop", "Stopped"
	if force {
		verb, done = "Kill", "Killed"
	}
	m.confirm = &confirmation{
		text:    fmt.Sprintf(":%d is forwarded by Docker to %s. %s that container?", port, name, verb),
		detail:  "Docker itself keeps running.",
		danger:  force,
		working: fmt.Sprintf("Stopping container %s…", name),
		yes: func() tea.Msg {
			if err := act.StopContainer(context.Background(), name, force); err != nil {
				return resultMsg{text: fmt.Sprintf("Couldn't stop %s: %v", name, err), err: true}
			}
			return resultMsg{text: fmt.Sprintf("%s container %s. Freed :%d.", done, name, port)}
		},
	}
}

// quitTarget is an app, or a lone process, to quit from the Connections or Memory screen.
type quitTarget struct {
	name    string
	appPath string // empty for processes outside an app
	main    model.ProcessID
	uid     int
}

// askQuit asks before quitting. Apps get a normal quit request, like ⌘Q; processes outside an
// app get SIGTERM. Force quit sends SIGKILL to the app's main process.
func (m *Model) askQuit(t quitTarget, force bool) {
	if err := m.deps.Actions.Refusal(t.main); err != nil {
		m.setMsg(err.Error(), warn)
		return
	}
	if t.appPath == "" {
		m.askKill(t.main, t.uid, force, fmt.Sprintf("%s (pid %d)", t.name, t.main.PID), "")
		return
	}
	if force {
		m.confirm = &confirmation{
			text:    fmt.Sprintf("Force quit %s (pid %d)? Unsaved work is lost.", t.name, t.main.PID),
			danger:  true,
			yes:     m.killCmd(t.main, true, t.name),
			working: fmt.Sprintf("Killing %s…", t.name),
		}
		return
	}
	act := m.deps.Actions
	m.confirm = &confirmation{
		text:    fmt.Sprintf("Quit %s?", t.name),
		detail:  "It gets a normal quit request, like ⌘Q.",
		working: fmt.Sprintf("Asking %s to quit…", t.name),
		yes: func() tea.Msg {
			ctx := context.Background()
			if _, err := act.Verify(ctx, t.main); err != nil {
				return actionError(t.name, err)
			}
			err := act.QuitApp(ctx, t.appPath)
			switch {
			case errors.Is(err, actions.ErrAutomationDenied):
				return automationDeniedMsg{target: t.main, label: t.name}
			case errors.Is(err, actions.ErrQuitCancelled):
				return resultMsg{text: fmt.Sprintf("%s stayed open.", t.name), quiet: true}
			case errors.Is(err, actions.ErrNotRegularApp):
				return resultMsg{text: fmt.Sprintf("%s isn't running as a regular app. Press X to force quit it.", t.name), warn: true}
			case err != nil:
				return resultMsg{text: fmt.Sprintf("Couldn't quit %s: %v", t.name, err), err: true}
			}
			return resultMsg{text: fmt.Sprintf("Asked %s to quit.", t.name)}
		},
	}
}
