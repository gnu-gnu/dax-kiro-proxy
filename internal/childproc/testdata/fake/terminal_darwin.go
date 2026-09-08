package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// These observations are authored from the public foreground-group and termios interfaces.
func terminalFixture(mode string) {
	fd := int(os.Stdin.Fd())
	group, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	if err != nil || group != syscall.Getpgrp() || group != os.Getpid() {
		os.Exit(31)
	}
	state, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		os.Exit(32)
	}
	state.Lflag &^= unix.ECHO | unix.ICANON
	if unix.IoctlSetTermios(fd, unix.TIOCSETA, state) != nil {
		os.Exit(33)
	}
	fmt.Println("independent-client-owns-terminal")
	switch mode {
	case "terminal-exit":
		return
	case "terminal-failure":
		os.Exit(23)
	case "terminal-hang":
		signal.Ignore(syscall.SIGTERM)
		for {
			time.Sleep(time.Second)
		}
	default:
		os.Exit(34)
	}
}
