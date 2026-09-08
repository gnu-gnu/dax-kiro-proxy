package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/relay/mcp"
	"dax-kiro-proxy/internal/schemacheck/worker"
)

func main() {
	if childproc.IsTerminalReclaimer(os.Args[1:]) {
		return
	}
	if len(os.Args) == 4 && os.Args[1] == "relay" && os.Args[2] == "--config" {
		config, err := relay.LoadChildConfig(os.Args[3])
		if err != nil {
			os.Exit(1)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if mcp.Run(ctx, os.Stdin, os.Stdout, config) != nil {
			os.Exit(1)
		}
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "schema-worker" {
		if worker.Run(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		return
	}
	fmt.Fprintln(os.Stderr, "usage: dax-kiro-proxy <command>")
	os.Exit(2)
}
