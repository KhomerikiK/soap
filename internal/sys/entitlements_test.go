package sys

import (
	"context"
	"errors"
	"slices"
	"testing"
)

const entitlementsXML = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>com.apple.security.app-sandbox</key><true/>
<key>com.apple.security.application-groups</key>
<array><string>UBF8T346G9.Office</string><string>group.com.example.shared</string></array>
<key>com.apple.security.files.user-selected.read-only</key><array><string>not-a-group</string></array>
</dict></plist>`

func TestAppGroups(t *testing.T) {
	want := "codesign -d --entitlements - --xml /Applications/Excel.app"
	run, calls := answer(t, want, entitlementsXML)
	got, err := AppGroups(context.Background(), run, "/Applications/Excel.app")
	if err != nil || !slices.Equal(got, []string{"UBF8T346G9.Office", "group.com.example.shared"}) {
		t.Errorf("groups = %v, %v", got, err)
	}
	if len(*calls) != 1 {
		t.Errorf("calls = %v", *calls)
	}
}

func TestAppGroupsNone(t *testing.T) {
	run, _ := answer(t, "codesign -d --entitlements - --xml /Applications/A.app",
		`<?xml version="1.0"?><plist version="1.0"><dict><key>x</key><true/></dict></plist>`)
	if got, err := AppGroups(context.Background(), run, "/Applications/A.app"); err != nil || len(got) != 0 {
		t.Errorf("groups = %v, %v", got, err)
	}
	run, _ = answer(t, "codesign -d --entitlements - --xml /Applications/A.app", "")
	if got, err := AppGroups(context.Background(), run, "/Applications/A.app"); err != nil || len(got) != 0 {
		t.Errorf("an app with no entitlements has no groups: %v, %v", got, err)
	}
}

func TestAppGroupsErrors(t *testing.T) {
	run := func(context.Context, string, ...string) ([]byte, error) {
		return nil, errors.New("code object is not signed")
	}
	if _, err := AppGroups(context.Background(), run, "/Applications/A.app"); err == nil {
		t.Error("a failed codesign is an error")
	}
	if _, err := AppGroups(context.Background(), run, "A.app"); err == nil {
		t.Error("a relative path is refused")
	}
}
