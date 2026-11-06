package sys

import (
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// OwnerDBURL is iptoasn.com's public-domain IP-to-owner dataset (about 11 MB).
const OwnerDBURL = "https://iptoasn.com/data/ip2asn-combined.tsv.gz"

// Owner is the network that an IP address belongs to.
type Owner struct {
	ASN     int
	Country string
	Name    string
}

// OwnerDB maps IP ranges to owners. Lookups are a binary search in memory; no IP leaves the Mac.
type OwnerDB struct {
	ranges []ipRange
	names  []string
}

type ipRange struct {
	start, end netip.Addr
	asn        int
	country    [2]byte
	name       int32 // index into OwnerDB.names
}

// DefaultOwnerDBPath is ~/Library/Caches/soap/ip2asn-combined.tsv.gz.
func DefaultOwnerDBPath() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "soap", "ip2asn-combined.tsv.gz"), nil
}

// ParseOwnerDB reads ip2asn's tab-separated format: start, end, AS number, country, description.
// Ranges with AS number 0 aren't routed and are skipped.
func ParseOwnerDB(r io.Reader) (*OwnerDB, error) {
	db := &OwnerDB{}
	nameIdx := map[string]int32{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; sc.Scan(); line++ {
		f := strings.Split(sc.Text(), "\t")
		if len(f) < 5 {
			return nil, &FormatError{Command: "ip2asn", Reason: fmt.Sprintf("line %d has %d fields, want 5", line, len(f))}
		}
		asn, err := strconv.Atoi(f[2])
		if err != nil {
			return nil, &FormatError{Command: "ip2asn", Reason: fmt.Sprintf("line %d: bad AS number %q", line, f[2])}
		}
		if asn == 0 {
			continue
		}
		start, err1 := netip.ParseAddr(f[0])
		end, err2 := netip.ParseAddr(f[1])
		if err1 != nil || err2 != nil {
			return nil, &FormatError{Command: "ip2asn", Reason: fmt.Sprintf("line %d: bad range %q to %q", line, f[0], f[1])}
		}
		i, ok := nameIdx[f[4]]
		if !ok {
			name := strings.Clone(f[4]) // don't keep the whole line alive
			i = int32(len(db.names))
			db.names = append(db.names, name)
			nameIdx[name] = i
		}
		var cc [2]byte
		copy(cc[:], f[3])
		db.ranges = append(db.ranges, ipRange{start: start, end: end, asn: asn, country: cc, name: i})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	sort.Slice(db.ranges, func(i, j int) bool { return db.ranges[i].start.Less(db.ranges[j].start) })
	return db, nil
}

// LoadOwnerDB reads a gzipped database file.
func LoadOwnerDB(path string) (*OwnerDB, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return ParseOwnerDB(zr)
}

// Len is the number of routed IP ranges.
func (db *OwnerDB) Len() int { return len(db.ranges) }

// Lookup returns the network that owns ip.
func (db *OwnerDB) Lookup(ip netip.Addr) (Owner, bool) {
	ip = ip.Unmap()
	i := sort.Search(len(db.ranges), func(i int) bool { return ip.Less(db.ranges[i].start) }) - 1
	if i < 0 {
		return Owner{}, false
	}
	r := db.ranges[i]
	if r.end.Less(ip) {
		return Owner{}, false
	}
	return Owner{ASN: r.asn, Country: strings.TrimRight(string(r.country[:]), "\x00"), Name: db.names[r.name]}, true
}

// UpdateOwnerDB downloads the database to path. The old file is replaced only after the download
// parses as a valid, non-empty database. It returns the number of ranges.
func UpdateOwnerDB(ctx context.Context, client *http.Client, url, path string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download failed: %s", resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "ip2asn-*.tmp")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name()) // a no-op once the file has been renamed
	_, copyErr := io.Copy(tmp, resp.Body)
	if err := errors.Join(copyErr, tmp.Close()); err != nil {
		return 0, err
	}
	db, err := LoadOwnerDB(tmp.Name())
	if err != nil {
		return 0, fmt.Errorf("the download isn't a valid IP database: %w", err)
	}
	if db.Len() == 0 {
		return 0, errors.New("the download contains no IP ranges")
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return 0, err
	}
	return db.Len(), nil
}
