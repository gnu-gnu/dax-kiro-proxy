//go:build !darwin

package main

import "os"

func terminalFixture(string) { os.Exit(35) }
