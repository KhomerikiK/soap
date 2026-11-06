//go:build darwin

package scan

import (
	"io/fs"
	"os"
	"syscall"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
)

// StampOf is what info, from Lstat, says about a file or folder, so a clean-up can tell later
// whether it changed.
func StampOf(info fs.FileInfo) model.Stamp {
	s := model.Stamp{Dir: info.IsDir(), Size: info.Size(), ModTime: info.ModTime()}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		s.Inode = st.Ino
	}
	return s
}

// StampPath is StampOf for path. A symlink is never followed.
func StampPath(path string) (model.Stamp, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return model.Stamp{}, err
	}
	return StampOf(info), nil
}

// madeTime is when a file was created, or its modified time when that isn't known.
func madeTime(info fs.FileInfo) time.Time {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Birthtimespec.Unix())
	}
	return info.ModTime()
}
