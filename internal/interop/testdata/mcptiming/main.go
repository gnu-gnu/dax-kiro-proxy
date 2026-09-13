// Independently authored MCP timing peer. Its only tool returns fixed text without an effect.
package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type configuration struct {
	GroupFile, Witness, Tool string
	DelayMilliseconds        int
	Observation              string
}

func peerBounds(cfg configuration) (limit, outer, delay time.Duration, ok bool) {
	if cfg.Observation == "" && cfg.DelayMilliseconds >= 1 && cfg.DelayMilliseconds <= 10000 {
		return 70 * time.Second, 75 * time.Second, time.Duration(cfg.DelayMilliseconds) * time.Millisecond, true
	}
	if cfg.Observation == "default-wait-135s" && cfg.DelayMilliseconds == 135000 {
		return 230 * time.Second, 235 * time.Second, 135 * time.Second, true
	}
	return 0, 0, 0, false
}

func readOwned(path string, limit int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, os.ErrInvalid
	}
	return io.ReadAll(io.LimitReader(f, limit+1))
}

func main() {
	if len(os.Args) != 2 || !filepath.IsAbs(os.Args[1]) {
		os.Exit(80)
	}
	data, err := readOwned(os.Args[1], 4096)
	var cfg configuration
	if err != nil || len(data) > 4096 || json.Unmarshal(data, &cfg) != nil || !filepath.IsAbs(cfg.GroupFile) || !filepath.IsAbs(cfg.Witness) || !member(cfg.Tool) {
		os.Exit(81)
	}
	limit, outer, delay, valid := peerBounds(cfg)
	if !valid {
		os.Exit(81)
	}
	// The observer publishes only its newly owned ACP leader. No wire request supplies a PID.
	groupData, err := readOwned(cfg.GroupFile, 32)
	group, parseErr := strconv.Atoi(strings.TrimSpace(string(groupData)))
	parentGroup, parentErr := syscall.Getpgid(os.Getppid())
	if err != nil || parseErr != nil || parentErr != nil || group <= 1 || group != parentGroup || syscall.Getpgrp() != group && syscall.Setpgid(0, group) != nil {
		os.Exit(82)
	}
	f, err := os.OpenFile(cfg.Witness, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		os.Exit(83)
	}
	defer f.Close()
	seq := 0
	started := time.Now()
	mark := func(kind string) bool {
		seq++
		if seq > 96 {
			return false
		}
		return json.NewEncoder(f).Encode(struct {
			PID, Group, Seq int
			Kind            string
			UnixNano, Nanos int64
		}{os.Getpid(), syscall.Getpgrp(), seq, kind, time.Now().UnixNano(), time.Since(started).Nanoseconds()}) == nil
	}
	if !mark("started") {
		os.Exit(84)
	}
	// This outer bound also covers an output pipe whose peer stops reading.
	lifetime := time.AfterFunc(outer, func() { os.Exit(85) })
	defer lifetime.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	maximum := 10 * time.Second
	if cfg.Observation != "" {
		maximum = 135 * time.Second
	}
	if !serveWithin(ctx, os.Stdin, os.Stdout, cfg.Tool, delay, maximum, mark) {
		os.Exit(86)
	}
}
