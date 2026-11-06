//go:build darwin

package scan

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>
#include <stdint.h>
#include <sys/attr.h>
#include <unistd.h>

static int mc_is_package(const char *path) {
	@autoreleasepool {
		NSString *p = [NSString stringWithUTF8String:path];
		if (p == nil) return -1;
		NSURL *u = [NSURL fileURLWithPath:p];
		NSNumber *v = nil;
		if (![u getResourceValue:&v forKey:NSURLIsPackageKey error:nil]) return -1;
		return [v boolValue] ? 1 : 0;
	}
}

struct fndr_buf {
	u_int32_t length;
	char      info[32];
} __attribute__((aligned(4), packed));

// mc_has_bundle_bit: 1 when the folder's Finder flags have kHasBundle (0x2000), 0 when not, and -1
// when the Finder info can't be read (or the reply is too short to hold it).
static int mc_has_bundle_bit(const char *path) {
	struct attrlist al;
	memset(&al, 0, sizeof(al));
	al.bitmapcount = ATTR_BIT_MAP_COUNT;
	al.commonattr = ATTR_CMN_FNDRINFO;
	struct fndr_buf buf;
	memset(&buf, 0, sizeof(buf));
	if (getattrlist(path, &al, &buf, sizeof(buf), FSOPT_NOFOLLOW) != 0) return -1;
	if (buf.length < sizeof(buf)) return -1;
	// For a folder, bytes 8-9 of the Finder info are the big-endian Finder flags.
	uint16_t flags = ((uint16_t)(unsigned char)buf.info[8] << 8) | (unsigned char)buf.info[9];
	return (flags & 0x2000) ? 1 : 0;
}
*/
import "C"

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

// IsPackage reports whether the folder at path is a package: an app, a library such as
// Photos Library.photoslibrary, or another bundle Finder shows as one file. A folder with a name on
// the fixed list (packageExts, or any extension ending in "library") is one without asking
// Foundation, because macOS doesn't know every app's bundles. For other names, Foundation is asked
// only about names with an extension (a dot that isn't the first character), because each
// question costs about 15 µs and a walk sees hundreds of thousands of folders. A folder without an
// extension is a package only when its Finder bundle bit is set (Photo Booth Library is one); that
// bit is read with getattrlist, which is cheap. If the Finder info can't be read, or the reply is
// too short to hold it, the folder is not known to be a package, so the answer is false.
func IsPackage(path string) bool {
	if strings.ContainsRune(path, 0) {
		return false
	}
	if packageByName(filepath.Base(path)) && isDir(path) {
		return true
	}
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	if strings.LastIndexByte(filepath.Base(path), '.') <= 0 {
		return C.mc_has_bundle_bit(cpath) == 1
	}
	return C.mc_is_package(cpath) == 1
}

// packageExts are extensions that always mean a package, whether or not macOS knows the app
// behind them (Lightroom isn't installed on every Mac, so Foundation says no to a .lrlibrary).
// Any extension that ends in "library" counts too.
var packageExts = map[string]bool{
	".app": true, ".bundle": true, ".framework": true, ".plugin": true, ".lrdata": true,
	".fcpbundle": true, ".logicx": true, ".band": true, ".rtfd": true, ".xcodeproj": true,
	".xcworkspace": true, ".pages": true, ".numbers": true, ".key": true,
}

// packageByName reports whether the name's extension, compared without case, marks a package.
func packageByName(name string) bool {
	if strings.LastIndexByte(name, '.') <= 0 {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	return packageExts[ext] || strings.HasSuffix(ext, "library")
}

func isDir(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir()
}

// PackageKind is the kind of package a name shows: "app", "library" or "package".
func PackageKind(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case ext == ".app":
		return "app"
	case strings.HasSuffix(ext, "library"):
		return "library"
	}
	return "package"
}
