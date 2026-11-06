//go:build darwin

package sys

import (
	"context"
	"os"
	"testing"

	"github.com/KhomerikiK/soap/internal/model"
)

func TestFootprint(t *testing.T) {
	fp, ok := Footprint(os.Getpid())
	if !ok || fp == 0 {
		t.Fatalf("Footprint(self) = %d, %v", fp, ok)
	}
	if os.Geteuid() != 0 {
		if _, ok := Footprint(1); ok {
			t.Error("Footprint(launchd) should be refused without root")
		}
	}
}

func TestCPUTicks(t *testing.T) {
	busy, total, err := CPUTicks()
	if err != nil {
		t.Fatal(err)
	}
	if total == 0 || busy > total {
		t.Fatalf("busy = %d, total = %d", busy, total)
	}
}

func TestInterfaceCounters(t *testing.T) {
	ifs, err := InterfaceCounters()
	if err != nil {
		t.Fatal(err)
	}
	if len(ifs) == 0 {
		t.Fatal("no interfaces")
	}
	if _, ok := ifs["lo0"]; ok {
		t.Error("loopback should be left out")
	}
}

func TestMachMemory(t *testing.T) {
	st, err := MachMemory{}.Stats(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Total == 0 || st.Used == 0 || st.Used > st.Total {
		t.Fatalf("stats = %+v", st)
	}
	if st.Pressure == model.PressureUnknown {
		t.Error("pressure level unknown")
	}
}
