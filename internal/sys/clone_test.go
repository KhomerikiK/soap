//go:build darwin

package sys

import (
	"crypto/rand"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestPrivateSizeOfClones needs the Mac's APFS disk, where t.TempDir lives.
func TestPrivateSizeOfClones(t *testing.T) {
	dir := t.TempDir()
	data := make([]byte, 8<<20)
	rand.Read(data)
	a, b, c := filepath.Join(dir, "a"), filepath.Join(dir, "b"), filepath.Join(dir, "c")
	if err := os.WriteFile(a, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if n, err := PrivateSize(a); err != nil || n < 8<<20 {
		t.Fatalf("a file of its own = %d, %v; want all of its 8 MB", n, err)
	}
	if out, err := exec.Command("cp", "-c", a, b).CombinedOutput(); err != nil {
		t.Skipf("cp -c can't clone here: %v %s", err, out)
	}
	na, errA := PrivateSize(a)
	nb, errB := PrivateSize(b)
	if errA != nil || errB != nil || na > 1<<20 || nb > 1<<20 {
		t.Errorf("after cp -c: a = %d (%v), b = %d (%v); a clone and its original share their blocks", na, errA, nb, errB)
	}
	if err := os.WriteFile(c, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if n, err := PrivateSize(c); err != nil || n < 8<<20 {
		t.Errorf("a plain copy = %d, %v; it shares nothing", n, err)
	}
	if _, err := PrivateSize(filepath.Join(dir, "nope")); err == nil {
		t.Error("a missing file should be an error")
	}
}
