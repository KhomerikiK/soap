//go:build darwin

package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/KhomerikiK/soap/internal/model"
	"github.com/KhomerikiK/soap/internal/sys"
)

type dfRow struct {
	Type        string `json:"Type"`
	Reclaimable string `json:"Reclaimable"`
}

type imageRow struct {
	Containers string `json:"Containers"`
	Repository string `json:"Repository"`
	Tag        string `json:"Tag"`
	Size       string `json:"Size"`
}

// findDocker offers Docker's own prunes. Volumes are never offered: they hold databases.
func findDocker(ctx context.Context, env Env) Result {
	if !env.hasTool("docker") {
		return Result{}
	}
	out, err := env.Run(ctx, "docker", "system", "df", "--format", "{{json .}}")
	if err != nil {
		var ce *sys.CommandError
		if errors.As(err, &ce) {
			return Result{Notes: []model.Note{{Screen: model.ScreenDev, Group: "Docker", Text: "Docker isn't running."}}}
		}
		return Result{Err: err}
	}
	reclaim := map[string]uint64{}
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var r dfRow
		err := dec.Decode(&r)
		if err == io.EOF {
			break
		}
		if err != nil {
			return Result{Err: fmt.Errorf("unexpected docker system df output: %w", err)}
		}
		n, err := parseDockerSize(r.Reclaimable)
		if err != nil {
			return Result{Err: err}
		}
		reclaim[r.Type] = n
	}
	dangling, danglingErr := danglingSize(ctx, env)
	unused, unusedErr := unusedImagesSize(ctx, env)
	if danglingErr == nil {
		danglingErr = unusedErr
	}
	var items []model.CleanItem
	add := func(id, name, detail string, size uint64, safe bool, cmd ...string) {
		if size == 0 {
			return
		}
		items = append(items, model.CleanItem{ID: "dev/docker/" + id, Screen: model.ScreenDev, Group: "Docker", Name: name, Detail: detail,
			Size: size, Method: model.MethodCommand, Command: cmd, Safe: safe})
	}
	add("build-cache", "Build cache", "rebuilt on the next docker build", reclaim["Build Cache"], true, "docker", "builder", "prune", "-a", "-f")
	if danglingErr == nil {
		add("untagged-images", "Untagged images", "leftovers from rebuilt images", dangling, true, "docker", "image", "prune", "-f")
		add("unused-images", "Images no container uses", "pulled or built again when needed", unused, false, "docker", "image", "prune", "-a", "-f")
	}
	add("stopped-containers", "Stopped containers", "docker compose recreates them; volumes stay", reclaim["Containers"], false, "docker", "container", "prune", "-f")
	notes := []model.Note{{Screen: model.ScreenDev, Group: "Docker", Text: "Volumes aren't offered: they hold your databases."}}
	if danglingErr == nil && unused == 0 && reclaim["Containers"] > 0 {
		notes = append(notes, model.Note{Screen: model.ScreenDev, Group: "Docker",
			Text: "Every image is used by a container. Remove stopped containers first, then the images they used can go."})
	}
	return Result{Items: items, Err: danglingErr, Notes: notes}
}

// danglingSize adds up untagged images that no container uses, which is what docker image prune
// removes.
func danglingSize(ctx context.Context, env Env) (uint64, error) {
	out, err := env.Run(ctx, "docker", "images", "--filter", "dangling=true", "--format", "{{json .}}")
	if err != nil {
		return 0, fmt.Errorf("couldn't list untagged docker images: %w", err)
	}
	var total uint64
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var r imageRow
		if dec.Decode(&r) != nil {
			break
		}
		if r.Containers != "0" && r.Containers != "N/A" && r.Containers != "" {
			continue
		}
		if n, err := parseDockerSize(r.Size); err == nil {
			total += n
		}
	}
	return total, nil
}

// unusedImagesSize adds up tagged images that no container uses, which is what docker image prune -a
// removes besides the untagged ones. It reads the images themselves because docker system df
// counts an image as reclaimable even when a stopped container keeps it.
func unusedImagesSize(ctx context.Context, env Env) (uint64, error) {
	out, err := env.Run(ctx, "docker", "images", "--format", "{{json .}}")
	if err != nil {
		return 0, fmt.Errorf("couldn't list docker images: %w", err)
	}
	var total uint64
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var r imageRow
		if err := dec.Decode(&r); err == io.EOF {
			break
		} else if err != nil {
			return 0, fmt.Errorf("unexpected docker images output: %w", err)
		}
		if r.Containers != "0" || (r.Repository == "<none>" && r.Tag == "<none>") {
			continue // used by a container, or untagged (its own row)
		}
		if n, err := parseDockerSize(r.Size); err == nil {
			total += n
		}
	}
	return total, nil
}

// parseDockerSize reads Docker's sizes, such as "3.749GB", "470.2MB (51%)" or "0B". Docker uses
// decimal units.
func parseDockerSize(s string) (uint64, error) {
	s, _, _ = strings.Cut(strings.TrimSpace(s), " ")
	units := []struct {
		suffix string
		mult   float64
	}{{"TB", 1e12}, {"GB", 1e9}, {"MB", 1e6}, {"kB", 1e3}, {"KB", 1e3}, {"B", 1}}
	for _, u := range units {
		if num, ok := strings.CutSuffix(s, u.suffix); ok {
			v, err := strconv.ParseFloat(num, 64)
			if err != nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
				break
			}
			return uint64(math.Round(v * u.mult)), nil
		}
	}
	return 0, fmt.Errorf("unexpected docker size %q", s)
}
