package actions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/KhomerikiK/soap/internal/model"
)

var (
	ErrNoEntry         = errors.New("no such history entry")
	ErrCantRestore     = errors.New("only items moved to the Trash can be put back")
	ErrAlreadyRestored = errors.New("this item was already put back")
	ErrRestoreTaken    = errors.New("something already exists where it used to be")
	ErrRestoreUnsafe   = errors.New("this history entry points somewhere soap doesn't put things back, so it wasn't put back")
)

// HistoryPath is where the clean-up log lives.
func HistoryPath(home string) string {
	return filepath.Join(home, "Library/Application Support/soap/history.jsonl")
}

// History is the clean-up log: one JSON object per line, oldest first.
type History struct {
	Path string
	Home string // restores only touch paths inside it
	mu   sync.Mutex
}

// CheckWritable makes sure entries can be added, creating the log's folder if needed.
func (h *History) CheckWritable() error {
	if err := os.MkdirAll(filepath.Dir(h.Path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(h.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	return f.Close()
}

// Append adds an entry to the end of the log.
func (h *History) Append(e model.HistoryEntry) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	// A crash can leave a last line without its newline; start a new line so it can't swallow this entry.
	f, err := os.OpenFile(h.Path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	if info, err := f.Stat(); err == nil && info.Size() > 0 {
		var last [1]byte
		if _, err := f.ReadAt(last[:], info.Size()-1); err == nil && last[0] != '\n' {
			b = append([]byte{'\n'}, b...)
		}
	}
	_, werr := f.Write(append(b, '\n'))
	return errors.Join(werr, f.Close())
}

// List returns every entry, newest first. A damaged line is skipped rather than losing the rest.
func (h *History) List() ([]model.HistoryEntry, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.read()
}

func (h *History) read() ([]model.HistoryEntry, error) {
	b, err := os.ReadFile(h.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []model.HistoryEntry
	for _, line := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e model.HistoryEntry
		if json.Unmarshal(line, &e) == nil {
			out = append(out, e)
		}
	}
	slices.Reverse(out)
	return out, nil
}

// Restore moves an entry's files back from the Trash to where they were and marks it restored.
// It changes nothing if any original location is taken or a trashed file is missing.
func (h *History) Restore(id string) (model.HistoryEntry, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	entries, err := h.read()
	if err != nil {
		return model.HistoryEntry{}, err
	}
	i := slices.IndexFunc(entries, func(e model.HistoryEntry) bool { return e.ID == id })
	if i < 0 {
		return model.HistoryEntry{}, ErrNoEntry
	}
	e := entries[i]
	if e.Restored {
		return e, ErrAlreadyRestored
	}
	if !e.CanRestore() || len(e.Trashed) > len(e.Paths) {
		return e, ErrCantRestore
	}
	if err := h.checkRestorable(e); err != nil {
		return e, err
	}
	for j, trashed := range e.Trashed {
		if _, err := os.Lstat(e.Paths[j]); err == nil {
			return e, fmt.Errorf("%w: %s", ErrRestoreTaken, e.Paths[j])
		}
		if _, err := os.Lstat(trashed); err != nil {
			return e, fmt.Errorf("%s is no longer in the Trash", filepath.Base(trashed))
		}
	}
	for j, trashed := range e.Trashed {
		if err := os.Rename(trashed, e.Paths[j]); err != nil {
			return e, err
		}
	}
	entries[i].Restored = true
	return entries[i], h.write(entries)
}

// checkRestorable refuses an entry whose paths aren't where soap put them. The history file
// isn't protected, so a changed line must not be able to move files anywhere.
func (h *History) checkRestorable(e model.HistoryEntry) error {
	if h.Home == "" || !filepath.IsAbs(h.Home) || filepath.Clean(h.Home) != h.Home {
		return ErrRestoreUnsafe
	}
	trashDir := filepath.Join(h.Home, ".Trash")
	for j, t := range e.Trashed {
		p := e.Paths[j]
		top := restoreTop(h.Home, p)
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || !strings.HasPrefix(p, top+"/") {
			return ErrRestoreUnsafe
		}
		if noSymlinkBetween(top, p) != nil {
			return ErrRestoreUnsafe
		}
		if filepath.Clean(t) != t || filepath.Dir(t) != trashDir || noSymlinkBetween(h.Home, t) != nil {
			return ErrRestoreUnsafe
		}
		info, err := os.Lstat(t)
		if err == nil && info.Mode()&fs.ModeSymlink != 0 {
			return ErrRestoreUnsafe
		}
		if top != h.Home { // an app: only a real folder named X.app, as the Cleaner moves it
			if err != nil || !info.IsDir() || !model.IsAppName(filepath.Base(p)) {
				return ErrRestoreUnsafe
			}
		}
	}
	return nil
}

// restoreTop is the folder that a path in the history must be inside to be put back: the home
// folder, or /Applications for an app that came from there.
func restoreTop(home, path string) string {
	if root := model.AppRoot(home, path); root != "" && !strings.HasPrefix(path, home+"/") {
		return root
	}
	return home
}

// write replaces the log with entries (given newest first), atomically.
func (h *History) write(newestFirst []model.HistoryEntry) error {
	var buf bytes.Buffer
	for i := len(newestFirst) - 1; i >= 0; i-- {
		b, err := json.Marshal(newestFirst[i])
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	tmp := h.Path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, h.Path)
}
