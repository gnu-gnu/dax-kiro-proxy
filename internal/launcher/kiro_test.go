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
	calls         []childproc.Command
	identity      string
	failure       error
	version       string
	helperVersion string
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
			version = "2.21.3"
		}
		if name == "kiro-cli-chat" && f.helperVersion != "" {
			version = f.helperVersion
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
	if len(f.calls) != 3 || info.Version != "2.21.3" || info.Executable != cfg.Executable || info.Helper != "/fixture/bin/kiro-cli-chat" || len(info.ProfileScope) != 64 || !info.HadPostamble {
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
func TestKiroPreflightAdmitsSameMajorBuildsAndReportsThem(t *testing.T) {
	cfg := launcher.KiroConfig{Executable: "/fixture/bin/kiro-cli", Home: t.TempDir(), Directory: t.TempDir(), ScopeKey: [32]byte{41}}
	for _, version := range []string{"2.21.1", "2.21.9", "2.99.0", "2.21"} {
		f := &preflightFixture{version: version, identity: `{"email":"x@example.invalid","accountType":"z"}`}
		info, err := launcher.CheckKiro(t.Context(), f, cfg)
		if err != nil || info.Version != version || len(f.calls) != 3 {
			t.Fatal("same-major Kiro build was rejected or misreported", version, err)
		}
	}
	for _, c := range []struct {
		name, output, version string
		ok                    bool
	}{
		{"kiro-cli", "kiro-cli 2.21.3\n", "2.21.3", true},
		{"kiro-cli-chat", "kiro-cli-chat 2.21.9", "2.21.9", true},
		{"kiro-cli", "kiro-cli-chat 2.21.3", "", false},
		{"kiro-cli", "kiro-cli 3.0.0", "", false},
		{"kiro-cli", "kiro-cli 2.21.3-beta", "", false},
		{"kiro-cli", "kiro-cli  2.21.3", "", false},
		{"kiro-cli", "kiro-cli 2.21.3 extra", "", false},
		{"kiro-cli", "kiro-cli " + strings.Repeat("2", 300), "", false},
	} {
		version, ok := launcher.KiroVersionFromOutput(c.name, []byte(c.output))
		if ok != c.ok || version != c.version || launcher.CompatibleKiroOutput(c.name, []byte(c.output)) != c.ok {
			t.Fatal("Kiro version admission differs", c.output)
		}
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
	if _, err := launcher.CheckKiro(t.Context(), f, cfg); !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, launcher.ErrLoginCheck) {
		t.Fatal("timeout lost its cause or was treated as a login failure")
	}
	for _, version := range []string{"3.0.0", "1.21.3", "2.21.3-beta", "v2.21.3", "2.21.3 extra"} {
		f = &preflightFixture{version: version}
		_, err := launcher.CheckKiro(t.Context(), f, cfg)
		if !errors.Is(err, launcher.ErrKiroVersion) || len(f.calls) != 1 {
			t.Fatal("unsupported Kiro version reached login lookup")
		}
		// Only a well-formed dotted build is named back in diagnostics; malformed output is not echoed.
		var named *launcher.VersionError
		parsed := version == "3.0.0" || version == "1.21.3"
		if errors.As(err, &named) != parsed || parsed && (named.Found != version || named.Component != "kiro-cli" || named.Expected != "major version 2") {
			t.Fatal("rejected build was not named safely", version, err)
		}
	}
	for _, version := range []string{"2.21.1", "2.21.9", "3.0.0"} {
		f = &preflightFixture{helperVersion: version}
		_, err := launcher.CheckKiro(t.Context(), f, cfg)
		var named *launcher.VersionError
		if !errors.Is(err, launcher.ErrKiroVersion) || len(f.calls) != 2 || !errors.As(err, &named) || named.Found != version || named.Component != "kiro-cli-chat" {
			t.Fatal("mismatched main/helper versions reached login lookup or were not named", version, err)
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
