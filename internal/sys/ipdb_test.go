package sys

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const ownerTSV = "1.0.0.0\t1.0.0.255\t13335\tUS\tCLOUDFLARENET\n" +
	"1.0.1.0\t1.0.3.255\t0\tNone\tNot routed\n" +
	"8.8.8.0\t8.8.8.255\t15169\tUS\tGOOGLE\n" +
	"2a00:1450::\t2a00:1450:ffff:ffff:ffff:ffff:ffff:ffff\t15169\tUS\tGOOGLE\n"

func gzipped(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	if _, err := zw.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestOwnerDBLookup(t *testing.T) {
	db, err := ParseOwnerDB(strings.NewReader(ownerTSV))
	if err != nil {
		t.Fatal(err)
	}
	if db.Len() != 3 {
		t.Fatalf("Len = %d, want 3 (unrouted ranges are skipped)", db.Len())
	}
	google := Owner{ASN: 15169, Country: "US", Name: "GOOGLE"}
	tests := []struct {
		ip    string
		want  Owner
		found bool
	}{
		{"1.0.0.1", Owner{ASN: 13335, Country: "US", Name: "CLOUDFLARENET"}, true},
		{"1.0.2.1", Owner{}, false},
		{"8.8.8.8", google, true},
		{"9.9.9.9", Owner{}, false},
		{"0.0.0.1", Owner{}, false},
		{"2a00:1450:4001::1", google, true},
		{"::ffff:8.8.8.8", google, true},
	}
	for _, tt := range tests {
		got, ok := db.Lookup(netip.MustParseAddr(tt.ip))
		if ok != tt.found || got != tt.want {
			t.Errorf("Lookup(%s) = %+v, %v; want %+v, %v", tt.ip, got, ok, tt.want, tt.found)
		}
	}
}

func TestParseOwnerDBRejectsBadLines(t *testing.T) {
	var fe *FormatError
	if _, err := ParseOwnerDB(strings.NewReader("1.0.0.0\tbad\n")); !errors.As(err, &fe) {
		t.Errorf("short line: got %v, want *FormatError", err)
	}
	if _, err := ParseOwnerDB(strings.NewReader("1.0.0.0\tnope\t13335\tUS\tX\n")); !errors.As(err, &fe) {
		t.Errorf("bad address: got %v, want *FormatError", err)
	}
}

func TestUpdateOwnerDB(t *testing.T) {
	body := gzipped(t, ownerTSV)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(body) }))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "soap", "ip2asn-combined.tsv.gz")
	n, err := UpdateOwnerDB(context.Background(), srv.Client(), srv.URL, path)
	if err != nil || n != 3 {
		t.Fatalf("UpdateOwnerDB = %d, %v", n, err)
	}
	db, err := LoadOwnerDB(path)
	if err != nil || db.Len() != 3 {
		t.Fatalf("LoadOwnerDB = %v, %v", db, err)
	}
}

func TestUpdateOwnerDBKeepsOldFileOnBadDownload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("<html>not a database</html>"))
	}))
	defer srv.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "ip2asn-combined.tsv.gz")
	if err := os.WriteFile(path, gzipped(t, ownerTSV), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateOwnerDB(context.Background(), srv.Client(), srv.URL, path); err == nil {
		t.Fatal("a bad download should fail")
	}
	if db, err := LoadOwnerDB(path); err != nil || db.Len() != 3 {
		t.Errorf("the old database should be kept: %v, %v", db, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("temporary file left behind: %v", entries)
	}
}

func TestUpdateOwnerDBHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	_, err := UpdateOwnerDB(context.Background(), srv.Client(), srv.URL, filepath.Join(t.TempDir(), "db.gz"))
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("got %v, want a 404 error", err)
	}
}
