package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"dax-kiro-proxy/internal/catalog"
	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/internal/inference"
	"dax-kiro-proxy/internal/installation"
	"dax-kiro-proxy/internal/launcher"
	"dax-kiro-proxy/internal/relay"
	"dax-kiro-proxy/internal/relay/mcp"
	"dax-kiro-proxy/internal/schemacheck/worker"
	"dax-kiro-proxy/internal/startupnotice"
	"dax-kiro-proxy/internal/status"
	"dax-kiro-proxy/internal/statusline"
	"dax-kiro-proxy/internal/turnnotice"
)

func main() {
	// Helpers acquire their own shared lease before dispatch. The parent also holds
	// its lease until child cleanup finishes, preserving executable paths for reexec.
	var lease io.Closer
	if len(os.Args) < 2 || (os.Args[1] != "install" && os.Args[1] != "uninstall") {
		executable, err := os.Executable()
		if err == nil {
			lease, err = installation.Lease(executable)
		}
		if err != nil {
			os.Exit(installationFailure(os.Stderr, err))
		}
		if lease != nil {
			defer lease.Close()
		}
	}
	if childproc.IsTerminalReclaimer(os.Args[1:]) {
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "schema-worker" {
		if worker.Run(os.Stdin, os.Stdout) != nil {
			os.Exit(1)
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// A UI helper owns no child process or durable state. Its whole-process deadline also
	// bounds a stalled inherited stdout or filesystem call, which context alone cannot interrupt.
	var displayDeadline *time.Timer
	if len(os.Args) == 4 && (os.Args[1] == "statusline" || os.Args[1] == "model-notice" || os.Args[1] == "turn-metrics") && os.Args[2] == "--config" {
		displayDeadline = time.AfterFunc(2*time.Second, func() { os.Exit(1) })
	}
	files := childproc.AttachedIO{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr, Foreground: true}
	services := commandServices{
		home: os.UserHomeDir, source: installationSource,
		install: installation.Install, uninstall: installation.Uninstall,
		defaults: defaultOptions, inspect: launcher.Inspect, run: launcher.Run,
		statusline: statusline.Display,
		notice:     startupnotice.Output,
		metrics:    turnnotice.Output,
		schema:     func() error { return worker.Run(os.Stdin, os.Stdout) },
		relay: func(ctx context.Context, path string) error {
			config, err := relay.LoadChildConfig(path)
			if err != nil {
				return err
			}
			attachment, err := relay.Attach(ctx, config)
			if err != nil {
				return err
			}
			defer attachment.Close()
			return mcp.Run(attachment.Context(), os.Stdin, os.Stdout, config)
		},
	}
	code := execute(ctx, os.Args[1:], files, os.Stdout, os.Stderr, services)
	if displayDeadline != nil {
		displayDeadline.Stop()
	}
	stop()
	if lease != nil {
		lease.Close()
	}
	os.Exit(code)
}

// Injection is local to command tests. CLI arguments cannot select an alternate launcher or policy.
type commandServices struct {
	home       func() (string, error)
	source     func() (string, error)
	install    func(context.Context, string, string, bool) error
	uninstall  func(context.Context, string) error
	defaults   func() (launcher.LaunchOptions, error)
	inspect    func(context.Context, launcher.LaunchOptions) (launcher.StartupReport, error)
	run        func(context.Context, launcher.LaunchOptions, childproc.AttachedIO) (launcher.LaunchResult, error)
	schema     func() error
	relay      func(context.Context, string) error
	statusline func(context.Context, string) (string, error)
	notice     func(context.Context, string) (string, error)
	metrics    func(context.Context, string) (string, error)
}

const helpText = `usage: dax-kiro-proxy <doctor|models|run|install|uninstall> [options]
  doctor           inspect startup requirements without launching the client
  models           list Kiro model aliases without a model turn
  run              launch the client when Kiro execution restrictions are verified
  version          show the development version
  install          install this executable and retained notices for the current user
  uninstall        remove an idle managed installation; preserve user settings
installation options:
  --bin-dir PATH   absolute installation directory (default: ~/.local/bin)
  --force          replace an intact idle installation (install only)
launch/diagnostic options:
  --kiro PATH      Kiro executable
  --client PATH    Claude Code executable
  --state-dir PATH product state directory
  --runtime-dir PATH temporary runtime parent
  --settings PATH  user settings source
  --model ID       exact Kiro backend model ID
  --effort VALUE   initial effort
  --timing         write measured startup phases to stderr
  --json           JSON output for doctor or models
run options:
  --client-history retain native conversation data in ~/.claude/projects
  --resume UUID    resume a native conversation; implies --client-history
`

func execute(ctx context.Context, args []string, files childproc.AttachedIO, out, diagnostics io.Writer, services commandServices) int {
	if childproc.IsTerminalReclaimer(args) {
		return 0
	}
	if len(args) == 1 && args[0] == "schema-worker" {
		if services.schema == nil || services.schema() != nil {
			return 1
		}
		return 0
	}
	if len(args) == 3 && args[0] == "relay" && args[1] == "--config" {
		if services.relay == nil || services.relay(ctx, args[2]) != nil {
			return 1
		}
		return 0
	}
	if len(args) == 3 && (args[0] == "statusline" || args[0] == "model-notice" || args[0] == "turn-metrics") && args[1] == "--config" {
		display := services.statusline
		limit := 1024
		if args[0] == "model-notice" {
			display = services.notice
		}
		if args[0] == "turn-metrics" {
			display, limit = services.metrics, status.MaxMetricsOutput
		}
		if display == nil {
			return 1
		}
		line, err := display(ctx, args[2])
		if err != nil || len(line) > limit {
			return 1
		}
		n, err := io.WriteString(out, line)
		if err != nil || n != len(line) {
			return 1
		}
		return 0
	}
	if !validArguments(args) {
		return usageError(diagnostics)
	}
	if len(args) == 1 {
		switch args[0] {
		case "help", "--help", "-h":
			return writeResult(out, diagnostics, []byte(helpText))
		case "version", "--version":
			return writeResult(out, diagnostics, []byte("dax-kiro-proxy development\n"))
		}
	}
	command := args[0]
	if command == "install" || command == "uninstall" {
		return executeInstallation(ctx, args, out, diagnostics, services)
	}
	if command != "doctor" && command != "models" && command != "run" {
		return usageError(diagnostics)
	}
	var options launcher.LaunchOptions
	var timing, structured bool
	flags := flag.NewFlagSet("dax-kiro-proxy", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.KiroExecutable, "kiro", "", "")
	flags.StringVar(&options.ClientExecutable, "client", "", "")
	flags.StringVar(&options.StateDirectory, "state-dir", "", "")
	flags.StringVar(&options.RuntimeParent, "runtime-dir", "", "")
	flags.StringVar(&options.UserSettings, "settings", "", "")
	flags.StringVar(&options.InitialModel, "model", "", "")
	flags.StringVar(&options.InitialEffort, "effort", "", "")
	flags.BoolVar(&timing, "timing", false, "")
	if command != "run" {
		flags.BoolVar(&structured, "json", false, "")
	} else {
		flags.BoolVar(&options.KeepHistory, "client-history", false, "")
		flags.StringVar(&options.ResumeSession, "resume", "", "")
	}
	if err := flags.Parse(args[1:]); errors.Is(err, flag.ErrHelp) {
		return writeResult(out, diagnostics, []byte(helpText))
	} else if err != nil || flags.NArg() != 0 {
		return usageError(diagnostics)
	}
	emptyOption := false
	flags.Visit(func(option *flag.Flag) {
		if option.Value.String() == "" {
			emptyOption = true
		}
		if option.Name == "client-history" && !options.KeepHistory && options.ResumeSession != "" {
			emptyOption = true
		}
	})
	if emptyOption {
		return usageError(diagnostics)
	}
	if options.ResumeSession != "" {
		options.KeepHistory = true
	}
	if services.defaults == nil {
		return failure(diagnostics, launcher.ErrConfig, launcher.ClientRunResult{})
	}
	defaults, err := services.defaults()
	if err != nil {
		return failure(diagnostics, launcher.ErrConfig, launcher.ClientRunResult{})
	}
	options.Home, options.Project, options.ProxyExecutable = defaults.Home, defaults.Project, defaults.ProxyExecutable
	options.Environment = append([]string{}, defaults.Environment...)
	options.Interactive = command != "models"
	for _, pair := range []struct {
		target   *string
		fallback string
	}{
		{&options.KiroExecutable, defaults.KiroExecutable}, {&options.ClientExecutable, defaults.ClientExecutable},
		{&options.StateDirectory, defaults.StateDirectory}, {&options.RuntimeParent, defaults.RuntimeParent},
		{&options.UserSettings, defaults.UserSettings},
	} {
		if *pair.target == "" {
			*pair.target = pair.fallback
		}
	}
	if ctx.Err() != nil {
		return failure(diagnostics, ctx.Err(), launcher.ClientRunResult{})
	}
	if command == "run" {
		if services.run == nil {
			return failure(diagnostics, launcher.ErrConfig, launcher.ClientRunResult{})
		}
		result, err := services.run(ctx, options, files)
		if timing && writeTimings(diagnostics, result.Startup.Phases) != nil {
			err = errors.Join(err, io.ErrClosedPipe)
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			err = errors.Join(err, context.Canceled)
		}
		return failure(diagnostics, err, result.Client)
	}
	if services.inspect == nil {
		return failure(diagnostics, launcher.ErrConfig, launcher.ClientRunResult{})
	}
	report, err := services.inspect(ctx, options)
	if timing && writeTimings(diagnostics, report.Phases) != nil {
		err = errors.Join(err, io.ErrClosedPipe)
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		err = errors.Join(err, context.Canceled)
	}
	if err != nil {
		return failure(diagnostics, err, launcher.ClientRunResult{})
	}
	var data []byte
	if structured {
		var value any = report
		if command == "models" {
			models := report.Models
			if models == nil {
				models = []inference.Model{}
			}
			value = struct {
				Object string            `json:"object"`
				Data   []inference.Model `json:"data"`
			}{"list", models}
		}
		data, err = json.Marshal(value)
		data = append(data, '\n')
	} else if command == "models" {
		for _, model := range report.Models {
			if !safeAlias(model.ID) {
				return failure(diagnostics, launcher.ErrModels, launcher.ClientRunResult{})
			}
			data = append(data, model.ID...)
			data = append(data, '\n')
		}
	} else {
		available := "unavailable"
		if report.LaunchAvailable {
			available = "available"
		}
		login, policy := "unchecked", "unchecked"
		if report.Login == "verified" {
			login = "verified"
		}
		if report.Policy == "verified" || report.Policy == "unverified" {
			policy = report.Policy
		}
		data = []byte(fmt.Sprintf("Login: %s\nExecution policy: %s\nLaunch: %s\nClient initialization: unverified\n", login, policy, available))
		if report.KiroVersion == launcher.SupportedKiroVersion {
			data = append(data, "Kiro: "+launcher.SupportedKiroVersion+"\n"...)
		}
		if report.ClientVersion == launcher.SupportedClientVersion {
			data = append(data, "Claude Code: "+launcher.SupportedClientVersion+"\n"...)
		}
		if safeAlias(report.SelectedModel) {
			data = append(data, "Selected model: "+report.SelectedModel+"\n"...)
		}
	}
	if err != nil || len(data) > 2<<20 {
		return failure(diagnostics, launcher.ErrModels, launcher.ClientRunResult{})
	}
	return writeResult(out, diagnostics, data)
}

func defaultOptions() (launcher.LaunchOptions, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return launcher.LaunchOptions{}, err
	}
	project, err := os.Getwd()
	if err != nil {
		return launcher.LaunchOptions{}, err
	}
	proxy, err := os.Executable()
	if err != nil {
		return launcher.LaunchOptions{}, err
	}
	return launcher.LaunchOptions{Home: home, Project: project, ProxyExecutable: proxy, RuntimeParent: os.TempDir(), StateDirectory: filepath.Join(home, ".dax-kiro-proxy"), UserSettings: filepath.Join(home, ".claude", "settings.json"), Environment: os.Environ()}, nil
}

func validArguments(args []string) bool {
	if len(args) == 0 || len(args) > 64 {
		return false
	}
	total := 0
	for _, arg := range args {
		if len(arg) > 4096 || !utf8.ValidString(arg) {
			return false
		}
		total += len(arg)
		if total > 16<<10 {
			return false
		}
		for _, r := range arg {
			if unicode.IsControl(r) {
				return false
			}
		}
	}
	return true
}

func safeAlias(alias string) bool {
	if !strings.HasPrefix(alias, "claude-dax-") || len(alias) > 128 {
		return false
	}
	for _, r := range alias {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func usageError(out io.Writer) int {
	fmt.Fprintln(out, "invalid command or options; run dax-kiro-proxy --help")
	return 2
}

func writeResult(out, diagnostics io.Writer, data []byte) int {
	n, err := out.Write(data)
	if err != nil || n != len(data) {
		fmt.Fprintln(diagnostics, "could not write command output")
		return 1
	}
	return 0
}

func writeTimings(out io.Writer, phases []launcher.PhaseTiming) error {
	if len(phases) > 32 {
		return launcher.ErrConfig
	}
	var data strings.Builder
	for _, phase := range phases {
		if phase.Milliseconds < 0 || phase.Milliseconds > 7*24*60*60*1000 {
			return launcher.ErrConfig
		}
		switch phase.Name {
		case "binary_settings", "runtime_preparation", "state", "client_version", "login_check", "model_catalog", "execution_policy", "backend_preparation", "gateway_startup", "client_profile", "process_launch", "runtime_cleanup":
			fmt.Fprintf(&data, "%s: %d ms\n", phase.Name, phase.Milliseconds)
		default:
			return launcher.ErrConfig
		}
	}
	n, err := io.WriteString(out, data.String())
	if err != nil {
		return err
	}
	if n != data.Len() {
		return io.ErrShortWrite
	}
	return nil
}

func failure(out io.Writer, err error, client launcher.ClientRunResult) int {
	if err == nil {
		if client.ClientPID > 0 && client.ExitCode > 0 && client.ExitCode <= 255 {
			return client.ExitCode
		}
		return 0
	}
	code, message := 1, "startup or client execution failed"
	cleanupFailed := errors.Is(err, launcher.ErrRunCleanup) || errors.Is(err, childproc.ErrCleanup) || errors.Is(err, childproc.ErrTerminal)
	switch {
	case errors.Is(err, context.Canceled):
		code, message = 130, "canceled"
	case errors.Is(err, launcher.ErrPolicyUnverified):
		code, message = 3, "launch unavailable: Kiro execution restrictions have not been verified"
	case errors.Is(err, launcher.ErrConfig), errors.Is(err, launcher.ErrSettings), errors.Is(err, catalog.ErrModel), errors.Is(err, childproc.ErrParameters):
		code, message = 2, "invalid or unsupported launch configuration"
	case errors.Is(err, launcher.ErrLoginCheck):
		message = "Kiro login could not be verified; run kiro-cli login and retry"
	case errors.Is(err, launcher.ErrKiroVersion):
		message = "Kiro installation or version is not supported"
	case errors.Is(err, launcher.ErrClientVersion):
		message = "Claude Code installation or version is not supported"
	case errors.Is(err, launcher.ErrModels), errors.Is(err, catalog.ErrCatalog):
		message = "model catalog preparation failed; retry dax-kiro-proxy doctor --timing to inspect startup stages"
	case errors.Is(err, launcher.ErrState):
		message = "private state preparation failed; check --state-dir ownership and permissions"
	case cleanupFailed:
		message = "could not complete client or runtime cleanup"
		if client.ClientPID > 0 && client.ExitCode > 0 && client.ExitCode <= 255 {
			code = client.ExitCode
		}
	case errors.Is(err, childproc.ErrExit):
		message = "client exited unsuccessfully"
		if client.ClientPID > 0 && client.ExitCode > 0 && client.ExitCode <= 255 {
			code = client.ExitCode
		}
	}
	fmt.Fprintln(out, message)
	if cleanupFailed && message != "could not complete client or runtime cleanup" {
		fmt.Fprintln(out, "could not complete client or runtime cleanup")
	}
	return code
}
