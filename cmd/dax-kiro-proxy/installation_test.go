package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/installation"
)

func TestInstallationDispatchDoesNotResolveLauncherOrProbeClients(t *testing.T) {
	for _, command := range []string{"install", "uninstall"} {
		for _, custom := range []bool{false, true} {
			called := 0
			services := commandServices{
				home: func() (string, error) {
					if custom {
						t.Fatal("custom location read HOME")
					}
					return "/owned/home", nil
				},
				source: func() (string, error) {
					if command != "install" {
						t.Fatal("uninstall resolved source")
					}
					return "/owned/source", nil
				},
			}
			want := "/owned/home/.local/bin"
			args := []string{command}
			if custom {
				want = "/owned/custom"
				args = append(args, "--bin-dir", want)
			}
			services.install = func(_ context.Context, bin, source string, force bool) error {
				called++
				if bin != want || source != "/owned/source" || force != custom {
					t.Fatal("install options changed")
				}
				return nil
			}
			services.uninstall = func(_ context.Context, bin string) error {
				called++
				if bin != want {
					t.Fatal("uninstall location changed")
				}
				return nil
			}
			if command == "install" && custom {
				args = append(args, "--force")
			}
			code, out, diagnostics := invoke(t, t.Context(), args, services)
			if code != 0 || called != 1 || out == "" || diagnostics != "" {
				t.Fatal("installation dispatch failed", code, called)
			}
		}
	}
}

func TestInstallationHelpAndInvalidOptionsHaveNoSideEffects(t *testing.T) {
	for _, args := range [][]string{{"install", "--help"}, {"uninstall", "--help"}} {
		if code, _, _ := invoke(t, t.Context(), args, commandServices{}); code != 0 {
			t.Fatal("installation help reached defaults")
		}
	}
	for _, args := range [][]string{
		{"uninstall", "--force"}, {"install", "--bin-dir="}, {"install", "--bin-dir", "relative"},
		{"install", "--source", "/owned/private"}, {"install", "--kiro", "/owned/private"},
		{"install", "--bin-dir", "/"}, {"uninstall", "unexpected"}, {"install", "--bin-dir", "/owned/../private"},
	} {
		code, out, diagnostics := invoke(t, t.Context(), args, commandServices{})
		if code != 2 || out != "" || strings.Contains(diagnostics, "private") {
			t.Fatal("invalid installation command admitted", code)
		}
	}
}

func TestInstallationErrorsAreFixedAndPartialPublicationIsExplicit(t *testing.T) {
	for _, tc := range []struct {
		err    error
		code   int
		phrase string
	}{
		{installation.ErrBusy, 1, "in use"}, {installation.ErrExists, 1, "--force"},
		{installation.ErrUnmanaged, 1, "changed files"}, {installation.ErrLocation, 2, "directory"},
		{context.Canceled, 130, "canceled"}, {errors.Join(context.Canceled, installation.ErrPublished), 1, "publication changed"},
		{errors.Join(installation.ErrIO, installation.ErrCleanup), 1, "cleanup is incomplete"},
	} {
		services := commandServices{uninstall: func(context.Context, string) error {
			return errors.Join(tc.err, errors.New("private-installation-sentinel"))
		}}
		code, out, diagnostics := invoke(t, t.Context(), []string{"uninstall", "--bin-dir", "/owned/bin"}, services)
		if code != tc.code || out != "" || !strings.Contains(diagnostics, tc.phrase) || strings.Contains(diagnostics, "private-installation-sentinel") {
			t.Fatal("installation outcome concealed or leaked", code, diagnostics)
		}
	}
}
