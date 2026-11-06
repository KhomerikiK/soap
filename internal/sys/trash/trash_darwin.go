//go:build darwin

// Package trash moves files to the user's Trash the way Finder does.
package trash

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>
#include <stdlib.h>
#include <string.h>

static int mc_trash(const char *path, char *out, int outlen, char *err, int errlen) {
	@autoreleasepool {
		NSString *p = [NSString stringWithUTF8String:path];
		if (p == nil) {
			strlcpy(err, "the path isn't valid UTF-8", errlen);
			return -1;
		}
		NSURL *url = [NSURL fileURLWithPath:p];
		NSURL *result = nil;
		NSError *e = nil;
		if (![[NSFileManager defaultManager] trashItemAtURL:url resultingItemURL:&result error:&e]) {
			strlcpy(err, e ? [[e localizedDescription] UTF8String] : "unknown error", errlen);
			return -1;
		}
		const char *where = result ? [[result path] fileSystemRepresentation] : NULL;
		strlcpy(out, where ? where : "", outlen);
		return 0;
	}
}
*/
import "C"

import (
	"fmt"
	"strings"
	"unsafe"
)

// Move moves path to the Trash, so Finder's "Put Back" works too, and returns where it went.
func Move(path string) (string, error) {
	if strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("couldn't move %q to the Trash: the path has a NUL byte", path)
	}
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))
	out := make([]byte, 4096)
	msg := make([]byte, 1024)
	outp := (*C.char)(unsafe.Pointer(&out[0]))
	msgp := (*C.char)(unsafe.Pointer(&msg[0]))
	if C.mc_trash(cpath, outp, C.int(len(out)), msgp, C.int(len(msg))) != 0 {
		return "", fmt.Errorf("couldn't move %s to the Trash: %s", path, C.GoString(msgp))
	}
	return C.GoString(outp), nil
}
