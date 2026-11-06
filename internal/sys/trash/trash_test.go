//go:build darwin

package trash

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMoveMissingFileFails(t *testing.T) {
	if _, err := Move(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("moving a missing file to the Trash should fail")
	}
}

// TestMoveAndPutBack uses the real Trash, so it runs only when asked for.
func TestMoveAndPutBack(t *testing.T) {
	if os.Getenv("SOAP_LIVE_TRASH") != "1" {
		t.Skip("set SOAP_LIVE_TRASH=1 to move a test file through the real Trash")
	}
	path := filepath.Join(t.TempDir(), "soap-trash-test.txt")
	if err := os.WriteFile(path, []byte("put me back"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest, err := Move(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); err == nil {
		t.Error("the file is still where it was")
	}
	if err := os.Rename(dest, path); err != nil {
		t.Fatalf("putting it back from %s: %v", dest, err)
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "put me back" {
		t.Errorf("after putting back: %q, %v", b, err)
	}
}
