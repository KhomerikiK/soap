package sys

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"strings"

	"github.com/KhomerikiK/soap/internal/model"
)

// netstatTail is the number of tokens after process:pid: state, options, gencnt, flags,
// flags1, usecnt, rtncnt, fltrs.
const netstatTail = 8

// NetstatSource reads every TCP and UDP socket, for all users, without sudo.
type NetstatSource struct {
	Run Runner
}

// Sockets runs `netstat -anvl -p tcp` and `netstat -anvl -p udp`. Without -l, netstat cuts IPv6
// addresses to 16 characters.
func (n NetstatSource) Sockets(ctx context.Context) ([]model.Socket, error) {
	var all []model.Socket
	for _, proto := range []string{"tcp", "udp"} {
		out, err := n.Run(ctx, "netstat", "-anvl", "-p", proto)
		if err != nil {
			return nil, fmt.Errorf("netstat -p %s: %w", proto, err)
		}
		socks, err := ParseNetstat(out)
		if err != nil {
			return nil, err
		}
		all = append(all, socks...)
	}
	return all, nil
}

// ParseNetstat parses `netstat -anv` output for one protocol family.
func ParseNetstat(out []byte) ([]model.Socket, error) {
	var socks []model.Socket
	sawHeader := false
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		if f[0] == "Proto" {
			if !strings.Contains(line, "rxbytes") || !strings.Contains(line, "process:pid") {
				return nil, &FormatError{Command: "netstat", Reason: "header is missing rxbytes or process:pid"}
			}
			sawHeader = true
			continue
		}
		if !strings.HasPrefix(f[0], "tcp") && !strings.HasPrefix(f[0], "udp") {
			continue
		}
		if !sawHeader {
			return nil, &FormatError{Command: "netstat", Reason: "socket rows before the header"}
		}
		s, err := parseNetstatRow(f)
		if err != nil {
			return nil, err
		}
		socks = append(socks, s)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !sawHeader {
		return nil, &FormatError{Command: "netstat", Reason: "no header line"}
	}
	return socks, nil
}

// parseNetstatRow reads: proto recv-q send-q local foreign [state] rxbytes txbytes rhiwat shiwat
// process:pid <8 more tokens>. UDP rows have no state. Process names may contain spaces, so
// process:pid is everything between the buffer sizes and the last 8 tokens.
func parseNetstatRow(f []string) (model.Socket, error) {
	proto := f[0]
	isTCP := strings.HasPrefix(proto, "tcp")
	head := 5
	if isTCP {
		head = 6
	}
	if minFields := head + 4 + 1 + netstatTail; len(f) < minFields {
		return model.Socket{}, &FormatError{Command: "netstat", Reason: fmt.Sprintf("row has %d fields, want at least %d: %q", len(f), minFields, strings.Join(f, " "))}
	}
	local, err := parseNetstatAddr(f[3], proto)
	if err != nil {
		return model.Socket{}, err
	}
	remote, err := parseNetstatAddr(f[4], proto)
	if err != nil {
		return model.Socket{}, err
	}
	state := ""
	if isTCP {
		state = f[5]
	}
	rx, err1 := strconv.ParseUint(f[head], 10, 64)
	tx, err2 := strconv.ParseUint(f[head+1], 10, 64)
	if err1 != nil || err2 != nil {
		return model.Socket{}, &FormatError{Command: "netstat", Reason: fmt.Sprintf("byte counts %q %q aren't numbers", f[head], f[head+1])}
	}
	procField := strings.Join(f[head+4:len(f)-netstatTail], " ")
	i := strings.LastIndexByte(procField, ':')
	if i < 0 {
		return model.Socket{}, &FormatError{Command: "netstat", Reason: fmt.Sprintf("process field %q has no pid", procField)}
	}
	pid, err := strconv.Atoi(procField[i+1:])
	if err != nil {
		return model.Socket{}, &FormatError{Command: "netstat", Reason: fmt.Sprintf("process field %q has a bad pid", procField)}
	}
	return model.Socket{
		Proto: proto, Local: local, Remote: remote, State: state,
		PID: pid, Command: procField[:i], RxBytes: rx, TxBytes: tx,
	}, nil
}

// parseNetstatAddr reads netstat's host.port form. "*" as a host means any address; "*.*" means
// no address at all.
func parseNetstatAddr(s, proto string) (netip.AddrPort, error) {
	if s == "*.*" {
		return netip.AddrPort{}, nil
	}
	i := strings.LastIndexByte(s, '.')
	if i < 0 {
		return netip.AddrPort{}, &FormatError{Command: "netstat", Reason: fmt.Sprintf("address %q has no port", s)}
	}
	host, portStr := s[:i], s[i+1:]
	var port uint16
	if portStr != "*" {
		p, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil {
			return netip.AddrPort{}, &FormatError{Command: "netstat", Reason: fmt.Sprintf("address %q has a bad port", s)}
		}
		port = uint16(p)
	}
	var addr netip.Addr
	if host == "*" {
		if strings.HasSuffix(proto, "4") {
			addr = netip.IPv4Unspecified()
		} else {
			addr = netip.IPv6Unspecified()
		}
	} else {
		a, err := netip.ParseAddr(host)
		if err != nil {
			return netip.AddrPort{}, &FormatError{Command: "netstat", Reason: fmt.Sprintf("address %q isn't an IP", s)}
		}
		addr = a
	}
	return netip.AddrPortFrom(addr, port), nil
}
