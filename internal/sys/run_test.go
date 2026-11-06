package sys

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestExecRunnerReturnsStdout(t *testing.T) {
	out, err := ExecRunner(5*time.Second)(context.Background(), "sh", "-c", "echo hi")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "hi\n" {
		t.Errorf("got %q", out)
	}
}

func TestExecRunnerSetsCLocale(t *testing.T) {
	out, err := ExecRunner(5*time.Second)(context.Background(), "sh", "-c", "echo $LC_ALL")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "C" {
		t.Errorf("LC_ALL = %q, want C", out)
	}
}

func TestExecRunnerWrapsFailures(t *testing.T) {
	_, err := ExecRunner(5*time.Second)(context.Background(), "sh", "-c", "echo oops >&2; exit 3")
	var ce *CommandError
	if !errors.As(err, &ce) {
		t.Fatalf("got %v, want *CommandError", err)
	}
	if ce.Stderr != "oops" {
		t.Errorf("Stderr = %q, want oops", ce.Stderr)
	}
	if !strings.Contains(err.Error(), "oops") {
		t.Errorf("Error() = %q should include stderr", err.Error())
	}
}

func TestExecRunnerTimesOut(t *testing.T) {
	_, err := ExecRunner(50*time.Millisecond)(context.Background(), "sleep", "2")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("got %v, want a timeout error", err)
	}
}
