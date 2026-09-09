// Independent effect-free client hook: record owned process identity, wait, then deny.
package main

import (
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) != 2 || !filepath.IsAbs(os.Args[1]) {
		os.Exit(2)
	}
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(interrupt)
	f, err := os.OpenFile(os.Args[1], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		os.Exit(2)
	}
	_, err = io.WriteString(f, strconv.Itoa(os.Getpid())+" "+strconv.Itoa(syscall.Getpgrp())+"\n")
	if closeErr := f.Close(); err != nil || closeErr != nil {
		os.Exit(2)
	}
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case <-interrupt:
	case <-timer.C:
	}
	os.Exit(2)
}
