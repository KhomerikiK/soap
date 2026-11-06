package sys

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// mdlsNull is what LastOpened asks mdls to print for an app without a last-opened date.
const mdlsNull = "(null)"

// LastOpened asks Spotlight when each app was last opened, with one mdls call for all of them. A
// zero time means Spotlight has no date for that app. mdls prints one value per app, in the order
// asked and without the file names, so an answer with any other number of values is an error.
func LastOpened(ctx context.Context, run Runner, apps []string) ([]time.Time, error) {
	if len(apps) == 0 {
		return nil, nil
	}
	for _, a := range apps {
		if !filepath.IsAbs(a) { // a relative path starting with "-" would be read as an option
			return nil, fmt.Errorf("%q isn't a full path", a)
		}
	}
	args := append([]string{"-raw", "-nullMarker", mdlsNull, "-name", "kMDItemLastUsedDate"}, apps...)
	out, err := run(ctx, "mdls", args...)
	if err != nil {
		return nil, err
	}
	vals := strings.Split(strings.TrimSuffix(string(out), "\n"), "\x00")
	if len(vals) != len(apps) {
		return nil, &FormatError{Command: "mdls", Reason: fmt.Sprintf("%d values for %d apps", len(vals), len(apps))}
	}
	dates := make([]time.Time, len(apps))
	for i, v := range vals {
		if v == mdlsNull {
			continue
		}
		t, err := time.Parse("2006-01-02 15:04:05 -0700", v)
		if err != nil {
			return nil, &FormatError{Command: "mdls", Reason: fmt.Sprintf("bad date %q", v)}
		}
		dates[i] = t
	}
	return dates, nil
}

// spotlightIDSep is what mdfind -attr puts between an app's path and its bundle ID.
const spotlightIDSep = "   kMDItemCFBundleIdentifier = "

// SpotlightApps lists the bundle IDs, in lowercase, of every app Spotlight knows on the Mac, with
// one mdfind call. No IDs means Spotlight is off or has no apps indexed.
func SpotlightApps(ctx context.Context, run Runner) (map[string]bool, error) {
	out, err := run(ctx, "mdfind", "-attr", "kMDItemCFBundleIdentifier", "kMDItemContentType == 'com.apple.application-bundle'")
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	lines, parsed := 0, 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines++
		i := strings.LastIndex(line, spotlightIDSep)
		if i < 0 {
			continue
		}
		parsed++
		if id := strings.TrimSpace(line[i+len(spotlightIDSep):]); id != "" && id != mdlsNull {
			ids[strings.ToLower(id)] = true
		}
	}
	if lines > 0 && parsed == 0 { // an answer in a format soap doesn't know isn't "Spotlight is off"
		return nil, &FormatError{Command: "mdfind", Reason: fmt.Sprintf("no line of %d had a bundle ID", lines)}
	}
	return ids, nil
}

// Reveal shows path, selected, in a Finder window, with open -R. It changes nothing.
func Reveal(ctx context.Context, run Runner, path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%q isn't a full path", path)
	}
	_, err := run(ctx, "open", "-R", path)
	return err
}
