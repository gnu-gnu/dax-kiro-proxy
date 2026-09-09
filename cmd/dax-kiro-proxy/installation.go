package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"dax-kiro-proxy/internal/installation"
)

func installationSource() (string, error) {
	source, err := os.Executable()
	if err != nil {
		return "", installation.ErrSource
	}
	source, err = filepath.EvalSymlinks(source)
	if err != nil {
		return "", installation.ErrSource
	}
	return source, nil
}

func executeInstallation(ctx context.Context, args []string, out, diagnostics io.Writer, services commandServices) int {
	var bin string
	var force bool
	flags := flag.NewFlagSet("installation", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&bin, "bin-dir", "", "")
	if args[0] == "install" {
		flags.BoolVar(&force, "force", false, "")
	}
	if err := flags.Parse(args[1:]); errors.Is(err, flag.ErrHelp) {
		return writeResult(out, diagnostics, []byte(helpText))
	} else if err != nil || flags.NArg() != 0 {
		return usageError(diagnostics)
	}
	invalid := false
	flags.Visit(func(option *flag.Flag) {
		if option.Name == "bin-dir" && option.Value.String() == "" {
			invalid = true
		}
	})
	if invalid || bin != "" && (!filepath.IsAbs(bin) || filepath.Clean(bin) != bin || bin == "/") {
		return usageError(diagnostics)
	}
	if err := ctx.Err(); err != nil {
		return installationFailure(diagnostics, err)
	}
	if bin == "" {
		if services.home == nil {
			return installationFailure(diagnostics, installation.ErrLocation)
		}
		home, err := services.home()
		if err != nil || !filepath.IsAbs(home) {
			return installationFailure(diagnostics, installation.ErrLocation)
		}
		bin = filepath.Join(home, ".local", "bin")
	}
	var err error
	if args[0] == "install" {
		if services.source == nil || services.install == nil {
			return installationFailure(diagnostics, installation.ErrSource)
		}
		source, sourceErr := services.source()
		if sourceErr != nil {
			return installationFailure(diagnostics, installation.ErrSource)
		}
		err = services.install(ctx, bin, source, force)
	} else {
		if services.uninstall == nil {
			return installationFailure(diagnostics, installation.ErrIO)
		}
		err = services.uninstall(ctx, bin)
	}
	if err != nil {
		return installationFailure(diagnostics, err)
	}
	message := "Installation complete.\n"
	if args[0] == "uninstall" {
		message = "Uninstall complete; user settings retained.\n"
	}
	return writeResult(out, diagnostics, []byte(message))
}

func installationFailure(out io.Writer, err error) int {
	code, message := 1, "installation filesystem operation failed"
	switch {
	case errors.Is(err, installation.ErrPublished):
		message = "installation publication changed; inspect retained artifacts before retrying"
	case errors.Is(err, installation.ErrBusy):
		message = "installation is in use; stop its processes before retrying"
	case errors.Is(err, installation.ErrExists):
		message = "installation already exists; use install --force to replace an intact idle installation"
	case errors.Is(err, installation.ErrUnmanaged):
		message = "installation contains unrecognized or changed files; preserved for inspection"
	case errors.Is(err, installation.ErrLocation):
		code, message = 2, "installation requires an absolute owned directory with safe permissions"
	case errors.Is(err, installation.ErrSource):
		message = "installation source is unavailable or unsafe"
	case errors.Is(err, context.Canceled):
		code, message = 130, "installation canceled"
	}
	fmt.Fprintln(out, message)
	if errors.Is(err, installation.ErrCleanup) {
		fmt.Fprintln(out, "installation cleanup is incomplete; retained artifacts require inspection")
	}
	return code
}
