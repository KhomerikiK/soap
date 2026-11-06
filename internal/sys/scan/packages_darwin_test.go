//go:build darwin

package scan

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestIsPackage checks the folders later tests rely on: on this Mac a new, empty Foo.app or
// Foo.photoslibrary counts as a package.
func TestIsPackage(t *testing.T) {
	dir := t.TempDir()
	for name, want := range map[string]bool{
		"Foo.app": true, "Foo.photoslibrary": true, ".hidden.app": true,
		"plain": false, "Foo.d": false, ".app": false, "Foo.zzzqqq": false,
	} {
		p := filepath.Join(dir, name)
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := IsPackage(p); got != want {
			t.Errorf("IsPackage(%s) = %v, want %v", name, got, want)
		}
	}
	file := filepath.Join(dir, "file.app")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if IsPackage(file) || IsPackage(filepath.Join(dir, "missing.app")) {
		t.Error("a file, or nothing at all, isn't a package")
	}
}

// TestIsPackageWithoutExtension checks a folder whose Finder bundle bit is set: macOS shows it as
// one file although its name has no extension (Photo Booth Library is one).
func TestIsPackageWithoutExtension(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "Plain Folder")
	bundle := filepath.Join(dir, "Bundle Folder")
	for _, p := range []string{plain, bundle} {
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat("/usr/bin/SetFile"); err != nil {
		t.Skip("SetFile isn't installed, so the bundle bit can't be set")
	}
	if out, err := exec.Command("/usr/bin/SetFile", "-a", "B", bundle).CombinedOutput(); err != nil {
		t.Fatalf("SetFile: %v %s", err, out)
	}
	if !IsPackage(bundle) {
		t.Error("a folder with the bundle bit is a package")
	}
	if IsPackage(plain) {
		t.Error("an ordinary folder is not a package")
	}
}

// TestIsPackageByExtension checks the names macOS may not know (Lightroom isn't installed, so
// Foundation says no to X.lrlibrary): the extension alone makes the folder a package.
func TestIsPackageByExtension(t *testing.T) {
	dir := t.TempDir()
	for name, want := range map[string]bool{
		"X.lrlibrary": true, "Y.FCPBUNDLE": true, "Z.imovielibrary": true, "W.Library": true,
		"notes.txt": false, "photos": false,
	} {
		p := filepath.Join(dir, name)
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if got := IsPackage(p); got != want {
			t.Errorf("IsPackage(%s) = %v, want %v", name, got, want)
		}
	}
}

func TestPackageKind(t *testing.T) {
	for name, want := range map[string]string{
		"zoom.us.app": "app", "Photos Library.photoslibrary": "library", "Foo.musiclibrary": "library",
		"Song.logicx": "package", "X.APP": "app", "Y.FCPBUNDLE": "package", "Z.imovielibrary": "library",
		"A.lrlibrary": "library", "B.xcodeproj": "package", "C.framework": "package",
	} {
		if got := PackageKind(name); got != want {
			t.Errorf("PackageKind(%s) = %q, want %q", name, got, want)
		}
	}
}

func TestStampPath(t *testing.T) {
	dir := t.TempDir()
	f := put(t, dir, "a.bin", 5000)
	s, err := StampPath(f)
	if err != nil || s.Dir || s.Size != 5000 || s.Inode == 0 || s.ModTime.IsZero() {
		t.Fatalf("file stamp = %+v, %v", s, err)
	}
	if again, _ := StampPath(f); !s.Same(again) {
		t.Error("an untouched file is the same")
	}
	later := s.ModTime.Add(time.Minute)
	if err := os.Chtimes(f, later, later); err != nil {
		t.Fatal(err)
	}
	if now, _ := StampPath(f); s.Same(now) {
		t.Error("a touched file has changed")
	}
	d, err := StampPath(dir)
	if err != nil || !d.Dir || d.Inode == 0 {
		t.Errorf("folder stamp = %+v, %v", d, err)
	}
	if _, err := StampPath(filepath.Join(dir, "nope")); err == nil {
		t.Error("a missing path should be an error")
	}
	info, _ := os.Lstat(f)
	if made := madeTime(info); made.IsZero() || time.Since(made) > time.Hour {
		t.Errorf("made = %v, want when the test wrote the file", made)
	}
}
