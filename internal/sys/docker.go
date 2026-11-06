package sys

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	"github.com/KhomerikiK/soap/internal/model"
)

// DockerSource lists running containers with the docker CLI, so it works with Docker Desktop and
// OrbStack alike.
type DockerSource struct {
	Run      Runner
	LookPath func(file string) (string, error) // exec.LookPath when nil
}

// Containers returns running containers. It returns model.ErrUnavailable when the docker CLI
// isn't installed or the daemon isn't running.
func (d DockerSource) Containers(ctx context.Context) ([]model.Container, error) {
	lookPath := d.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if _, err := lookPath("docker"); err != nil {
		return nil, model.ErrUnavailable
	}
	out, err := d.Run(ctx, "docker", "ps", "--format", "{{json .}}")
	if err != nil {
		var ce *CommandError
		if errors.As(err, &ce) {
			return nil, model.ErrUnavailable
		}
		return nil, err
	}
	return ParseDockerPS(out)
}

type dockerRow struct {
	ID    string `json:"ID"`
	Names string `json:"Names"`
	Ports string `json:"Ports"`
}

// ParseDockerPS parses one JSON object per line from `docker ps --format '{{json .}}'`.
func ParseDockerPS(out []byte) ([]model.Container, error) {
	var cs []model.Container
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var r dockerRow
		err := dec.Decode(&r)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, &FormatError{Command: "docker ps", Reason: err.Error()}
		}
		ports, err := parseDockerPorts(r.Ports)
		if err != nil {
			return nil, err
		}
		cs = append(cs, model.Container{ID: r.ID, Name: strings.Split(r.Names, ",")[0], Ports: ports})
	}
	return cs, nil
}

// parseDockerPorts reads Docker's port list, such as
// "0.0.0.0:80->80/tcp, [::]:80->80/tcp, 9000/tcp". Ports without "->" aren't published on the
// host and are skipped. IPv4 and IPv6 entries for the same port are merged.
func parseDockerPorts(s string) ([]model.PublishedPort, error) {
	var out []model.PublishedPort
	seen := map[model.PublishedPort]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		arrow := strings.Index(part, "->")
		if arrow < 0 {
			continue
		}
		host := part[:arrow]
		slash := strings.LastIndexByte(part, '/')
		colon := strings.LastIndexByte(host, ':')
		if slash < 0 || colon < 0 {
			return nil, &FormatError{Command: "docker ps", Reason: fmt.Sprintf("port mapping %q", part)}
		}
		lo, hi, err := parsePortRange(host[colon+1:])
		if err != nil {
			return nil, &FormatError{Command: "docker ps", Reason: fmt.Sprintf("port mapping %q: %v", part, err)}
		}
		proto := part[slash+1:]
		for p := uint32(lo); p <= uint32(hi); p++ {
			pp := model.PublishedPort{HostPort: uint16(p), Proto: proto}
			if !seen[pp] {
				seen[pp] = true
				out = append(out, pp)
			}
		}
	}
	return out, nil
}

func parsePortRange(s string) (uint16, uint16, error) {
	loStr, hiStr, isRange := strings.Cut(s, "-")
	lo, err := strconv.ParseUint(loStr, 10, 16)
	if err != nil {
		return 0, 0, err
	}
	if !isRange {
		return uint16(lo), uint16(lo), nil
	}
	hi, err := strconv.ParseUint(hiStr, 10, 16)
	if err != nil {
		return 0, 0, err
	}
	if hi < lo {
		return 0, 0, fmt.Errorf("range %s ends before it starts", s)
	}
	return uint16(lo), uint16(hi), nil
}
