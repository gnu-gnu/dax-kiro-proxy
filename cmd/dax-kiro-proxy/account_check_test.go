package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/launcher"
)

type accountCommandFixture struct {
	cause error
	exit  int
}

func (r accountCommandFixture) Run(_ context.Context, command childproc.Command) (childproc.Result, error) {
	if strings.Join(command.Args, " ") == "--version" {
		name := "kiro-cli"
		if strings.HasSuffix(command.Executable, "kiro-cli-chat") {
			name += "-chat"
		}
		return childproc.Result{Stdout: []byte(name + " " + launcher.SupportedKiroVersion)}, nil
	}
	return childproc.Result{ExitCode: r.exit, Stdout: []byte(`{"email":"command@example.invalid","accountType":"synthetic"}`)}, r.cause
}

func TestAccountCheckDiagnosticsKeepCauseAndCleanup(t *testing.T) {
	for _, tc := range []struct {
		name, message string
		cause         error
		exit, code    int
	}{
		{"deadline", "Kiro account check timed out", context.DeadlineExceeded, -1, 1},
		{"launch", "Kiro account check could not complete", childproc.ErrStart, -1, 1},
		{"output", "Kiro account check could not complete", childproc.ErrOutputLimit, 0, 1},
		{"unknown", "Kiro account check could not complete", errors.New("private-account-diagnostic"), 0, 1},
		{"nonzero", "Kiro login could not be verified; run kiro-cli login and retry", childproc.ErrExit, 1, 1},
		{"canceled", "canceled", context.Canceled, -1, 130},
	} {
		for _, cleanup := range []bool{false, true} {
			for _, command := range []string{"doctor", "models", "run"} {
				name := tc.name + "-" + command
				if cleanup {
					name += "-cleanup"
				}
				t.Run(name, func(t *testing.T) {
					cause := tc.cause
					if cleanup {
						cause = errors.Join(cause, childproc.ErrCleanup)
					}
					_, err := launcher.CheckKiro(t.Context(), accountCommandFixture{cause, tc.exit}, launcher.KiroConfig{Executable: "/fixture/bin/kiro-cli", Home: t.TempDir(), Directory: t.TempDir(), ScopeKey: [32]byte{53}})
					services := fixtureServices()
					services.inspect = func(context.Context, launcher.LaunchOptions) (launcher.StartupReport, error) {
						return fixtureReport(), err
					}
					services.run = func(context.Context, launcher.LaunchOptions, childproc.AttachedIO) (launcher.LaunchResult, error) {
						return launcher.LaunchResult{}, err
					}
					code, out, diagnostic := invoke(t, t.Context(), []string{command}, services)
					if code != tc.code || out != "" || !strings.HasPrefix(diagnostic, tc.message+"\n") && !strings.HasPrefix(diagnostic, tc.message+";") {
						t.Fatal("account command diagnostic or exit class differs")
					}
					if strings.Contains(diagnostic, "kiro-cli login") != (tc.name == "nonzero") || strings.Contains(diagnostic, "cleanup") != cleanup || strings.Contains(diagnostic, "private-account") || len(diagnostic) > 512 {
						t.Fatal("account diagnostic misdirected login, hid cleanup or exposed private data")
					}
				})
			}
		}
	}
}
