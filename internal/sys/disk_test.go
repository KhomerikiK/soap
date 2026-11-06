//go:build darwin

package sys

import (
	"os"
	"testing"
)

func TestDiskFree(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	free, total, err := DiskFree(home)
	if err != nil {
		t.Fatal(err)
	}
	if total == 0 || free == 0 || free > total {
		t.Errorf("free = %d, total = %d", free, total)
	}
	if _, _, err := DiskFree("/no/such/path"); err == nil {
		t.Error("a missing path should be an error")
	}
}
