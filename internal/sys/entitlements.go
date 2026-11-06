package sys

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
)

// groupsEntitlement is the entitlement that lists the app groups an app shares data in.
const groupsEntitlement = "com.apple.security.application-groups"

// AppGroups lists the app groups an app declares, from its code signature's entitlements, with
// codesign. No groups, and an app without entitlements, give an empty list; an error means
// codesign failed (an unsigned app, for one) or its answer isn't a property list.
func AppGroups(ctx context.Context, run Runner, app string) ([]string, error) {
	if !filepath.IsAbs(app) { // a relative path starting with "-" would be read as an option
		return nil, fmt.Errorf("%q isn't a full path", app)
	}
	out, err := run(ctx, "codesign", "-d", "--entitlements", "-", "--xml", app)
	if err != nil {
		return nil, err
	}
	if i := bytes.Index(out, []byte("<?xml")); i > 0 { // codesign can print a line before the plist
		out = out[i:]
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, nil
	}
	dec := xml.NewDecoder(bytes.NewReader(out))
	dec.Strict = false
	var groups []string
	inKey, want, inArray := false, false, false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return groups, nil
		}
		if err != nil {
			return nil, &FormatError{Command: "codesign", Reason: err.Error()}
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch {
			case t.Name.Local == "key":
				inKey = true
			case t.Name.Local == "array" && want:
				inArray = true
			case t.Name.Local == "string" && inArray:
				var s string
				if err := dec.DecodeElement(&s, &t); err != nil {
					return nil, &FormatError{Command: "codesign", Reason: err.Error()}
				}
				groups = append(groups, s)
			case t.Name.Local != "key" && !inArray:
				want = false
			}
		case xml.CharData:
			if inKey {
				want = string(bytes.TrimSpace(t)) == groupsEntitlement
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "key":
				inKey = false
			case "array":
				if inArray {
					return groups, nil
				}
			}
		}
	}
}
