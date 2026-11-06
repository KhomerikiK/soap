package sys

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseNetstatTCP(t *testing.T) {
	socks, err := ParseNetstat(readFixture(t, "netstat_tcp.txt"))
	if err != nil {
		t.Fatal(err)
	}
	ap := netip.MustParseAddrPort
	want := []model.Socket{
		{Proto: "tcp4", Local: ap("127.0.0.1:631"), State: "LISTEN", PID: 90823, Command: "cupsd"},
		{Proto: "tcp6", Local: ap("[::1]:631"), State: "LISTEN", PID: 90823, Command: "cupsd"},
		{Proto: "tcp4", Local: ap("127.0.0.1:47413"), State: "LISTEN", PID: 87752, Command: "ArtCraft Launche"},
		{Proto: "tcp46", Local: ap("[::]:60050"), State: "LISTEN", PID: 638, Command: "rapportd"},
		{Proto: "tcp4", Local: ap("192.168.1.20:51727"), Remote: ap("104.18.18.125:443"), State: "ESTABLISHED", PID: 83931, Command: "Grok Bot", RxBytes: 8176, TxBytes: 1249},
		{Proto: "tcp4", Local: ap("192.168.1.20:51801"), Remote: ap("140.82.114.26:443"), State: "ESTABLISHED", PID: 70396, Command: "Google Chrome He", RxBytes: 8016, TxBytes: 3504},
		{Proto: "tcp6", Local: ap("[2a00:1450::1]:51000"), Remote: ap("[2a00:1450:4001:82a::200e]:443"), State: "ESTABLISHED", PID: 555, Command: "Safari", RxBytes: 100, TxBytes: 200},
		{Proto: "tcp6", Local: ap("[fe80::1a1d:9423:70a4:237e%utun4]:1028"), Remote: ap("[fe80::878f:532:bd8c:5fa1%utun4]:1025"), State: "ESTABLISHED", PID: 665, Command: "identityservices", RxBytes: 6428, TxBytes: 6892},
	}
	if len(socks) != len(want) {
		t.Fatalf("got %d sockets, want %d", len(socks), len(want))
	}
	for i := range want {
		if socks[i] != want[i] {
			t.Errorf("socket %d:\n got  %+v\n want %+v", i, socks[i], want[i])
		}
	}
}

func TestParseNetstatUDP(t *testing.T) {
	socks, err := ParseNetstat(readFixture(t, "netstat_udp.txt"))
	if err != nil {
		t.Fatal(err)
	}
	ap := netip.MustParseAddrPort
	want := []model.Socket{
		{Proto: "udp4", Local: ap("192.168.1.20:54248"), Remote: ap("203.0.113.143:443"), PID: 72234, Command: "Brave Browser He", RxBytes: 10860568, TxBytes: 121589},
		{Proto: "udp4", Local: ap("0.0.0.0:5353"), PID: 83375, Command: "adb", TxBytes: 99},
		{Proto: "udp46", PID: 402, Command: "mDNSResponder"},
	}
	if len(socks) != len(want) {
		t.Fatalf("got %d sockets, want %d", len(socks), len(want))
	}
	for i := range want {
		if socks[i] != want[i] {
			t.Errorf("socket %d:\n got  %+v\n want %+v", i, socks[i], want[i])
		}
	}
}

func TestParseNetstatRejectsChangedHeader(t *testing.T) {
	_, err := ParseNetstat(readFixture(t, "netstat_badheader.txt"))
	var fe *FormatError
	if !errors.As(err, &fe) {
		t.Fatalf("got %v, want *FormatError", err)
	}
}

func TestParseNetstatRejectsShortRow(t *testing.T) {
	in := "Proto Recv-Q Send-Q Local Foreign (state) rxbytes txbytes rhiwat shiwat process:pid\n" +
		"tcp4 0 0 127.0.0.1.631 *.* LISTEN 0 0\n"
	_, err := ParseNetstat([]byte(in))
	var fe *FormatError
	if !errors.As(err, &fe) {
		t.Fatalf("got %v, want *FormatError", err)
	}
}

func TestParseNetstatRejectsMissingHeader(t *testing.T) {
	_, err := ParseNetstat([]byte("nothing useful here\n"))
	var fe *FormatError
	if !errors.As(err, &fe) {
		t.Fatalf("got %v, want *FormatError", err)
	}
}

func TestNetstatSourceRunsTCPThenUDP(t *testing.T) {
	var calls [][]string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		if args[len(args)-1] == "tcp" {
			return readFixture(t, "netstat_tcp.txt"), nil
		}
		return readFixture(t, "netstat_udp.txt"), nil
	}
	socks, err := NetstatSource{Run: run}.Sockets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(socks) != 11 {
		t.Errorf("got %d sockets, want 11", len(socks))
	}
	if len(calls) != 2 || calls[0][2] != "-p" || calls[0][3] != "tcp" || calls[1][3] != "udp" {
		t.Errorf("calls = %v", calls)
	}
}

func TestNetstatSourceLive(t *testing.T) {
	if testing.Short() {
		t.Skip("runs netstat")
	}
	socks, err := NetstatSource{Run: ExecRunner(5 * time.Second)}.Sockets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(socks) == 0 {
		t.Fatal("netstat returned no sockets")
	}
}
