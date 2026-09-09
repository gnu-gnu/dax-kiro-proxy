//go:build darwin || linux

package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"

	"dax-kiro-proxy/internal/childproc"
	"dax-kiro-proxy/third_party/notices"
)

func TestCompiledInstallationAndHelperLeaseOutsideRepository(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	build, err := childproc.New(childproc.Config{Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer build.Close()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	result, err := build.Run(t.Context(), childproc.Command{Executable: filepath.Join(runtime.GOROOT(), "bin", "go"), Directory: cwd, Environment: os.Environ(), Args: []string{"build", "-o", source, "."}})
	if err != nil || result.ExitCode != 0 {
		t.Fatal("owned command build failed", err, result.ExitCode)
	}
	home, project := filepath.Join(root, "home"), filepath.Join(root, "project")
	protected := []string{"home/.claude/settings.json", "home/.claude.json", "home/.kiro/settings/cli.json", "home/.dax-kiro-proxy/preferences", "project/.claude/settings.local.json", "home/.local/bin/unrelated"}
	for _, name := range protected {
		path := filepath.Join(root, name)
		if os.MkdirAll(filepath.Dir(path), 0700) != nil || os.WriteFile(path, []byte("Independent preserved settings."), 0600) != nil {
			t.Fatal("fixture")
		}
	}
	environment := []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	commands, err := childproc.New(childproc.Config{Timeout: 10 * time.Second, MaxOutputBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	defer commands.Close()
	run := func(binary string, code int, args ...string) {
		t.Helper()
		result, err := commands.Run(t.Context(), childproc.Command{Executable: binary, Directory: project, Environment: environment, Args: args})
		if result.ExitCode != code || code == 0 && err != nil || code != 0 && !errors.Is(err, childproc.ErrExit) {
			t.Fatal("compiled installation outcome differs", result.ExitCode, err)
		}
		if !errors.Is(syscall.Kill(-result.PID, 0), syscall.ESRCH) {
			t.Fatal("installation process group survived")
		}
	}
	run(source, 0, "install")
	public := filepath.Join(home, ".local", "bin", "dax-kiro-proxy")
	first, err := filepath.EvalSymlinks(public)
	if err != nil {
		t.Fatal("installed entry point absent")
	}
	sourceData, err := os.ReadFile(source)
	if err != nil {
		t.Fatal("source absent")
	}
	installedData, err := os.ReadFile(first)
	if err != nil || sha256.Sum256(sourceData) != sha256.Sum256(installedData) {
		t.Fatal("installed command bytes changed")
	}
	noticeCount := 0
	if err := fs.WalkDir(notices.Files, ".", func(name string, item fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if item.IsDir() {
			return nil
		}
		want, err := notices.Files.ReadFile(name)
		if err != nil {
			return err
		}
		path := filepath.Join(filepath.Dir(first), "notices", name)
		got, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode().Perm() != 0600 || !bytes.Equal(got, want) {
			t.Fatal("compiled notice retention differs")
		}
		noticeCount++
		return nil
	}); err != nil || noticeCount != 7 {
		t.Fatal("notice inventory differs", err, noticeCount)
	}
	run(public, 0, "--help")
	run(public, 1, "install")
	input, send, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	read, output, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []*os.File{input, send, read, output, null} {
		t.Cleanup(func() { file.Close() })
	}
	attached, err := childproc.NewAttached(childproc.AttachedConfig{Lifetime: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer attached.Close()
	worker, err := attached.Start(t.Context(), childproc.Command{Executable: public, Directory: project, Environment: environment, Args: []string{"schema-worker"}}, childproc.AttachedIO{Stdin: input, Stdout: output, Stderr: null})
	if err != nil {
		t.Fatal("installed helper start failed", err)
	}
	defer worker.Close()
	if _, err := send.Write([]byte("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\",\"params\":{\"protocolVersion\":1}}\n")); err != nil {
		t.Fatal(err)
	}
	if read.SetReadDeadline(time.Now().Add(5*time.Second)) != nil {
		t.Fatal("read deadline")
	}
	line, err := bufio.NewReaderSize(read, 4096).ReadSlice('\n')
	var ready struct {
		ID     int
		Result struct{ ProtocolVersion int }
	}
	if err != nil || json.Unmarshal(line, &ready) != nil || ready.ID != 1 || ready.Result.ProtocolVersion != 1 {
		t.Fatal("installed helper did not initialize")
	}
	run(source, 1, "install", "--force")
	run(public, 1, "uninstall")
	if current, err := filepath.EvalSymlinks(public); err != nil || current != first {
		t.Fatal("busy process path changed")
	}
	send.Close()
	result, err = worker.Wait()
	if err != nil || result.ExitCode != 0 || !errors.Is(syscall.Kill(-worker.PID(), 0), syscall.ESRCH) {
		t.Fatal("installed helper cleanup failed", err)
	}
	run(public, 0, "install", "--force")
	second, err := filepath.EvalSymlinks(public)
	if err != nil || second == first {
		t.Fatal("self reinstall modified mapped executable in place")
	}
	if _, err := os.Lstat(filepath.Dir(first)); !os.IsNotExist(err) {
		t.Fatal("retired command remained")
	}
	run(public, 0, "uninstall")
	run(source, 0, "uninstall")
	for _, path := range []string{public, filepath.Join(filepath.Dir(public), ".dax-kiro-proxy-install")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("installation artifact survived")
		}
	}
	for _, name := range protected {
		path := filepath.Join(root, name)
		data, err := os.ReadFile(path)
		info, statErr := os.Lstat(path)
		if err != nil || statErr != nil || string(data) != "Independent preserved settings." || info.Mode().Perm() != 0600 {
			t.Fatal("compiled installation changed protected state")
		}
	}
	if data, err := os.ReadFile(source); err != nil || sha256.Sum256(data) != sha256.Sum256(sourceData) {
		t.Fatal("installation changed source binary")
	}
	t.Log("single binary: seven exact notices, helper initialization, busy replacement/removal, self reinstall/uninstall, source/settings preservation and joined groups")
}
