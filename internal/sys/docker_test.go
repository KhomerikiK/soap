package sys

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/KhomerikiK/soap/internal/model"
)

func TestParseDockerPS(t *testing.T) {
	cs, err := ParseDockerPS(readFixture(t, "docker_ps.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	tcp := func(p uint16) model.PublishedPort { return model.PublishedPort{HostPort: p, Proto: "tcp"} }
	udp := func(p uint16) model.PublishedPort { return model.PublishedPort{HostPort: p, Proto: "udp"} }
	want := []model.Container{
		{ID: "95d08bc16fea", Name: "store-backend-laravel.test-1", Ports: []model.PublishedPort{tcp(80), tcp(5173)}},
		{ID: "6d0dcd6624ff", Name: "store-backend-mysql-1", Ports: []model.PublishedPort{tcp(3306)}},
		{ID: "aa11bb22cc33", Name: "worker-1"},
		{ID: "dd44ee55ff66", Name: "range-1", Ports: []model.PublishedPort{udp(8000), udp(8001), udp(8002)}},
	}
	if !reflect.DeepEqual(cs, want) {
		t.Errorf("got  %+v\nwant %+v", cs, want)
	}
}

func TestParseDockerPSRejectsBadJSON(t *testing.T) {
	_, err := ParseDockerPS([]byte("{not json"))
	var fe *FormatError
	if !errors.As(err, &fe) {
		t.Fatalf("got %v, want *FormatError", err)
	}
}

func TestDockerSourceWithoutDocker(t *testing.T) {
	src := DockerSource{
		Run:      func(context.Context, string, ...string) ([]byte, error) { t.Fatal("must not run"); return nil, nil },
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
	}
	if _, err := src.Containers(context.Background()); !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("got %v, want ErrUnavailable", err)
	}
}

func TestDockerSourceWithDaemonStopped(t *testing.T) {
	src := DockerSource{
		Run: func(context.Context, string, ...string) ([]byte, error) {
			return nil, &CommandError{Name: "docker", Err: errors.New("exit status 1"), Stderr: "Cannot connect to the Docker daemon"}
		},
		LookPath: func(string) (string, error) { return "/usr/local/bin/docker", nil },
	}
	if _, err := src.Containers(context.Background()); !errors.Is(err, model.ErrUnavailable) {
		t.Fatalf("got %v, want ErrUnavailable", err)
	}
}

func TestDockerSourceRunsPS(t *testing.T) {
	var ran string
	src := DockerSource{
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			ran = name + " " + strings.Join(args, " ")
			return readFixture(t, "docker_ps.jsonl"), nil
		},
		LookPath: func(string) (string, error) { return "/usr/local/bin/docker", nil },
	}
	cs, err := src.Containers(context.Background())
	if err != nil || len(cs) != 4 {
		t.Fatalf("got %d containers, %v", len(cs), err)
	}
	if ran != "docker ps --format {{json .}}" {
		t.Errorf("ran %q", ran)
	}
}
