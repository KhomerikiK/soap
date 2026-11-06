// Package sys reads system data on macOS. It's the only package that runs commands or makes
// system calls; everything it returns is a plain struct from package model.
package sys

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Runner runs a command and returns its standard output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// CommandError is a command that exited with an error.
type CommandError struct {
	Name   string
	Err    error
	Stderr string
}

func (e *CommandError) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("%s: %v: %s", e.Name, e.Err, e.Stderr)
	}
	return fmt.Sprintf("%s: %v", e.Name, e.Err)
}

func (e *CommandError) Unwrap() error { return e.Err }

// FormatError means a command's output didn't look the way soap expects.
type FormatError struct {
	Command string
	Reason  string
}

func (e *FormatError) Error() string {
	return fmt.Sprintf("unexpected %s output: %s", e.Command, e.Reason)
}

// ExecRunner runs real commands with the C locale and the given timeout. On failure it returns
// whatever stdout the command produced along with the error.
func ExecRunner(timeout time.Duration) Runner {
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Env = append(os.Environ(), "LC_ALL=C")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("%s timed out after %s", name, timeout)
		}
		if err != nil {
			return out, &CommandError{Name: name, Err: err, Stderr: strings.TrimSpace(stderr.String())}
		}
		return out, nil
	}
}
