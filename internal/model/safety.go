package model

var protectedNames = map[string]string{
	"kernel_task":  "it's the macOS kernel",
	"launchd":      "it starts and supervises every other process",
	"WindowServer": "quitting it would log you out",
	"loginwindow":  "quitting it would log you out",
}

// Protected reports whether soap must refuse to stop id, and why.
func Protected(id ProcessID, self int) (string, bool) {
	if id.PID == self {
		return "it's soap itself", true
	}
	if r, ok := protectedNames[id.Name]; ok {
		return r, true
	}
	if id.PID <= 1 {
		return "it's a core part of macOS", true
	}
	return "", false
}

// SameProcess reports whether cur is still the process the user saw as seen. macOS reuses PIDs,
// so the name and start time must match too.
func SameProcess(seen, cur ProcessID) bool {
	return seen.PID == cur.PID && seen.Name == cur.Name && seen.StartTime.Equal(cur.StartTime)
}
