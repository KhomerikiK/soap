package model

import (
	"testing"
	"time"
)

func TestProtected(t *testing.T) {
	tests := []struct {
		id   ProcessID
		want bool
	}{
		{ProcessID{PID: 0, Name: "kernel_task"}, true},
		{ProcessID{PID: 1, Name: "launchd"}, true},
		{ProcessID{PID: 1, Name: "weird"}, true}, // PID 1 is protected whatever it's called
		{ProcessID{PID: 400, Name: "WindowServer"}, true},
		{ProcessID{PID: 410, Name: "loginwindow"}, true},
		{ProcessID{PID: 999, Name: "soap"}, true}, // soap's own PID
		{ProcessID{PID: 48121, Name: "node"}, false},
	}
	for _, tt := range tests {
		reason, got := Protected(tt.id, 999)
		if got != tt.want {
			t.Errorf("Protected(%s) = %v, want %v", tt.id.Name, got, tt.want)
		}
		if got && reason == "" {
			t.Errorf("%s is protected without a reason", tt.id.Name)
		}
	}
}

func TestSameProcess(t *testing.T) {
	a := ProcessID{PID: 5, Name: "node", StartTime: t0}
	if !SameProcess(a, a) {
		t.Error("identical IDs should match")
	}
	b := a
	b.StartTime = t0.Add(time.Second)
	if SameProcess(a, b) {
		t.Error("a different start time means a reused PID")
	}
	c := a
	c.Name = "python3"
	if SameProcess(a, c) {
		t.Error("a different name means a different process")
	}
}
