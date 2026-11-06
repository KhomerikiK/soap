package sys

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/KhomerikiK/soap/internal/model"
)

// psFormat lists the columns soap reads. comm is last because paths contain spaces; lstart
// is five words, such as "Wed Nov  4 09:00:01 2026".
const psFormat = "pid=,ppid=,uid=,%cpu=,rss=,lstart=,comm="

// PSSource reads processes with ps, which is setuid on macOS and can see every process.
type PSSource struct {
	Run Runner
	// Footprint returns a process's memory footprint, the figure Activity Monitor shows. When it
	// returns false (for example for root's processes), the resident size is used instead.
	Footprint func(pid int) (uint64, bool)

	users userCache
}

// Processes lists every process.
func (s *PSSource) Processes(ctx context.Context) ([]model.Process, error) {
	out, err := s.Run(ctx, "ps", "-axww", "-o", psFormat)
	if err != nil {
		return nil, err
	}
	procs, err := ParsePS(out)
	if err != nil {
		return nil, err
	}
	s.fill(procs)
	return procs, nil
}

// Lookup reads one process again. It returns model.ErrNoProcess when the PID is gone.
func (s *PSSource) Lookup(ctx context.Context, pid int) (model.Process, error) {
	out, err := s.Run(ctx, "ps", "-ww", "-o", psFormat, "-p", strconv.Itoa(pid))
	if err != nil {
		var ce *CommandError
		if errors.As(err, &ce) && len(bytes.TrimSpace(out)) == 0 {
			return model.Process{}, model.ErrNoProcess
		}
		return model.Process{}, err
	}
	procs, err := ParsePS(out)
	if err != nil {
		return model.Process{}, err
	}
	if len(procs) == 0 {
		return model.Process{}, model.ErrNoProcess
	}
	s.fill(procs[:1])
	return procs[0], nil
}

// Args returns the full command line of each PID that still exists.
func (s *PSSource) Args(ctx context.Context, pids []int) (map[int]string, error) {
	if len(pids) == 0 {
		return map[int]string{}, nil
	}
	ids := make([]string, len(pids))
	for i, p := range pids {
		ids[i] = strconv.Itoa(p)
	}
	out, err := s.Run(ctx, "ps", "-ww", "-o", "pid=,args=", "-p", strings.Join(ids, ","))
	if err != nil && len(out) == 0 {
		return nil, err
	}
	// ps exits with status 1 when some PIDs are gone but still prints the rest.
	return ParsePSArgs(out), nil
}

func (s *PSSource) fill(procs []model.Process) {
	for i := range procs {
		procs[i].User = s.users.name(procs[i].UID)
		if s.Footprint != nil {
			if fp, ok := s.Footprint(procs[i].PID); ok {
				procs[i].Memory = fp
			}
		}
	}
}

// ParsePS parses `ps -o pid=,ppid=,uid=,%cpu=,rss=,lstart=,comm=` output.
func ParsePS(out []byte) ([]model.Process, error) {
	var procs []model.Process
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		f, comm, ok := cutFields(line, 10)
		if !ok || comm == "" {
			return nil, &FormatError{Command: "ps", Reason: fmt.Sprintf("short row %q", line)}
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		uid, err3 := strconv.Atoi(f[2])
		cpu, err4 := strconv.ParseFloat(f[3], 64)
		rss, err5 := strconv.ParseUint(f[4], 10, 64)
		if err := errors.Join(err1, err2, err3, err4, err5); err != nil {
			return nil, &FormatError{Command: "ps", Reason: fmt.Sprintf("row %q: %v", line, err)}
		}
		start, err := time.ParseInLocation("Mon Jan 2 15:04:05 2006", strings.Join(f[5:10], " "), time.Local)
		if err != nil {
			return nil, &FormatError{Command: "ps", Reason: fmt.Sprintf("row %q: bad start time", line)}
		}
		p := model.Process{
			ProcessID: model.ProcessID{PID: pid, Name: filepath.Base(comm), StartTime: start},
			PPID:      ppid, UID: uid, CPU: cpu, Memory: rss * 1024,
		}
		if strings.HasPrefix(comm, "/") {
			p.Path = comm
		}
		procs = append(procs, p)
	}
	return procs, sc.Err()
}

// ParsePSArgs parses `ps -o pid=,args=` output into a map from PID to command line.
func ParsePSArgs(out []byte) map[int]string {
	args := map[int]string{}
	for _, line := range strings.Split(string(out), "\n") {
		f, rest, ok := cutFields(line, 1)
		if !ok {
			continue
		}
		if pid, err := strconv.Atoi(f[0]); err == nil {
			args[pid] = rest
		}
	}
	return args
}

// cutFields splits off the first n whitespace-separated fields and returns the rest of the line
// with its inner spacing kept.
func cutFields(s string, n int) ([]string, string, bool) {
	fields := make([]string, 0, n)
	for len(fields) < n {
		s = strings.TrimLeft(s, " \t")
		if s == "" {
			return nil, "", false
		}
		i := strings.IndexAny(s, " \t")
		if i < 0 {
			fields = append(fields, s)
			s = ""
			continue
		}
		fields = append(fields, s[:i])
		s = s[i:]
	}
	return fields, strings.TrimLeft(s, " \t"), true
}

type userCache struct {
	mu sync.Mutex
	m  map[int]string
}

func (c *userCache) name(uid int) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[int]string{}
	}
	if n, ok := c.m[uid]; ok {
		return n
	}
	n := strconv.Itoa(uid)
	if u, err := user.LookupId(n); err == nil {
		n = u.Username
	}
	c.m[uid] = n
	return n
}
