package launcher_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/launcher"
)

type preflightFixture struct {
	calls    []childproc.Command
	identity string
	failure  error
	version  string
}

func (f *preflightFixture) Run(ctx context.Context, c childproc.Command) (childproc.Result, error) {
	f.calls = append(f.calls, c)
	if ctx.Err() != nil {
		return childproc.Result{ExitCode: -1}, ctx.Err()
	}
	if len(c.Args) == 1 && c.Args[0] == "--version" {
		name := "kiro-cli"
		if strings.HasSuffix(c.Executable, "kiro-cli-chat") {
			name += "-chat"
		}
		version := f.version
		if version == "" {
			version = "2.21.1"
		}
		return childproc.Result{ExitCode: 0, Stdout: []byte(name + " " + version + "\n")}, nil
	}
	return childproc.Result{ExitCode: 0, Stdout: []byte(f.identity)}, f.failure
}
func TestKiroPreflightPinsExecutablesAndKeepsAccountDataOutOfResults(t *testing.T) {
	cfg := launcher.KiroConfig{Executable: "/fixture/bin/kiro-cli", Home: t.TempDir(), Directory: t.TempDir(), ScopeKey: [32]byte{41}}
	f := &preflightFixture{identity: `{"email":"synthetic@example.invalid","accountType":"synthetic-account","region":"fixture-region","startUrl":"https://tenant.invalid"}` + "\n\nIndependent harmless CLI postamble.\n"}
	info, err := launcher.CheckKiro(t.Context(), f, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 3 || info.Version != "2.21.1" || info.Executable != cfg.Executable || info.Helper != "/fixture/bin/kiro-cli-chat" || len(info.ProfileScope) != 64 || !info.HadPostamble {
		t.Fatal("incorrect preflight sequence or scope")
	}
	for _, c := range f.calls {
		env := envMap(c.Environment)
		if env["HOME"] != cfg.Home || env["PATH"] != "/fixture/bin:/usr/bin:/bin:/usr/sbin:/sbin" || len(env) != 5 {
			t.Fatal("Kiro inherited uncontrolled environment")
		}
	}
	if strings.Join(f.calls[2].Args, " ") != "whoami --format json" {
		t.Fatal("preflight changed authentication instead of reading identity")
	}
	again, err := launcher.CheckKiro(t.Context(), f, cfg)
	if err != nil || again.ProfileScope != info.ProfileScope {
		t.Fatal("same account scope is unstable")
	}
	cfg.ScopeKey = [32]byte{42}
	changed, err := launcher.CheckKiro(t.Context(), f, cfg)
	if err != nil || changed.ProfileScope == info.ProfileScope {
		t.Fatal("profile identity is not keyed")
	}
	f.identity = `{"email":"other@example.invalid","accountType":"synthetic-account","region":"fixture-region","startUrl":"https://tenant.invalid"}`
	other, err := launcher.CheckKiro(t.Context(), f, cfg)
	if err != nil || other.ProfileScope == changed.ProfileScope {
		t.Fatal("different login identity shared a cache scope")
	}
}
func TestKiroPreflightNeverConfusesInvalidOutputOrTimeoutWithKnownLogin(t *testing.T) {
	cfg := launcher.KiroConfig{Executable: "/fixture/bin/kiro-cli", Home: t.TempDir(), Directory: t.TempDir(), ScopeKey: [32]byte{41}}
	for _, raw := range []string{`{}`, `null`, `"not an identity"`, `{"email":"x","email":"y","accountType":"z"}`, `{"email":"x","accountType":false}`, `{"email":"x","accountType":"z"}` + "\n{}", `{"email":"x","accountType":"z"}` + "\n{incomplete", strings.Repeat(" ", 64<<10) + `{}`} {
		f := &preflightFixture{identity: raw}
		if _, err := launcher.CheckKiro(t.Context(), f, cfg); !errors.Is(err, launcher.ErrLoginCheck) {
			t.Fatal("ambiguous CLI identity was accepted or reported as logged out")
		}
	}
	f := &preflightFixture{identity: `{"email":"x","accountType":"z"}`, failure: context.DeadlineExceeded}
	if _, err := launcher.CheckKiro(t.Context(), f, cfg); !errors.Is(err, launcher.ErrLoginCheck) {
		t.Fatal("timeout was treated as identity evidence")
	}
	for _, version := range []string{"2.21.2", "3.0.0"} {
		f = &preflightFixture{version: version}
		if _, err := launcher.CheckKiro(t.Context(), f, cfg); !errors.Is(err, launcher.ErrKiroVersion) || len(f.calls) != 1 {
			t.Fatal("unsupported Kiro version reached login lookup")
		}
	}
	cfg.ScopeKey = [32]byte{}
	if _, err := launcher.CheckKiro(t.Context(), f, cfg); !errors.Is(err, launcher.ErrConfig) {
		t.Fatal("unkeyed identity digest accepted")
	}
	cfg.ScopeKey = [32]byte{41}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	before := len(f.calls)
	if _, err := launcher.CheckKiro(ctx, f, cfg); !errors.Is(err, context.Canceled) || len(f.calls) != before {
		t.Fatal("canceled preflight started a command or became a login error")
	}
}
