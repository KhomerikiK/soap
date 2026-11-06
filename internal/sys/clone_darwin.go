//go:build darwin

package sys

/*
#include <sys/attr.h>
#include <unistd.h>
#include <errno.h>
#include <string.h>
#include <stdlib.h>

struct privsize_buf {
	u_int32_t length;
	off_t     privsize;
} __attribute__((aligned(4), packed));

static long long mc_private_size(const char *path) {
	struct attrlist al;
	memset(&al, 0, sizeof(al));
	al.bitmapcount = ATTR_BIT_MAP_COUNT;
	al.forkattr = ATTR_CMNEXT_PRIVATESIZE;
	struct privsize_buf buf;
	memset(&buf, 0, sizeof(buf));
	if (getattrlist(path, &al, &buf, sizeof(buf), FSOPT_ATTR_CMN_EXTENDED | FSOPT_NOFOLLOW) != 0) {
		return -1;
	}
	if (buf.length < sizeof(buf)) { // the volume didn't return the attribute: unknown, not 0
		errno = ENOTSUP;
		return -1;
	}
	return (long long)buf.privsize;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"
)

// PrivateSize is how many bytes of a file no other file shares: what removing it frees once the
// Trash is emptied. An APFS clone (Finder's Duplicate, cp -c) shares its blocks with the file it
// was cloned from, so both report close to 0. A symlink is never followed.
func PrivateSize(path string) (uint64, error) {
	if strings.ContainsRune(path, 0) {
		return 0, fmt.Errorf("%q has a NUL byte", path)
	}
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	n, err := C.mc_private_size(cpath) // cgo returns errno as err
	if n < 0 {
		if err == nil {
			err = errors.New("getattrlist failed")
		}
		return 0, fmt.Errorf("couldn't read how much of %s is shared: %w", path, err)
	}
	return uint64(n), nil
}
