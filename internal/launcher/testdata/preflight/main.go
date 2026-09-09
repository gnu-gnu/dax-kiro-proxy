// Independently authored CLI boundary fixture. It reports only synthetic version/account/catalog
// data, has no ACP/model command, and records fixed command names in its explicitly supplied HOME.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	name := filepath.Base(os.Args[0])
	args := strings.Join(os.Args[1:], " ")
	label := ""
	output := ""
	switch {
	case name == "kiro-cli" && args == "--version":
		label = "kiro-version"
		output = "kiro-cli 2.21.2"
	case name == "kiro-cli-chat" && args == "--version":
		label = "helper-version"
		output = "kiro-cli-chat 2.21.2"
	case name == "kiro-cli" && args == "whoami --format json":
		label = "identity"
		output = `{"accountType":"synthetic","email":"startup-fixture@example.invalid"}`
	case name == "kiro-cli" && args == "chat --list-models --format json":
		label = "catalog"
		output = `{"default_model":"fixture-backend","models":[{"model_id":"fixture-backend","model_name":"Independent fixture"}]}`
	default:
		os.Exit(60)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), "preflight-slow")); err == nil {
		time.Sleep(150 * time.Millisecond)
	}
	file, err := os.OpenFile(filepath.Join(os.Getenv("HOME"), "preflight-observations"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		os.Exit(61)
	}
	_, err = fmt.Fprintln(file, label)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		os.Exit(62)
	}
	fmt.Println(output)
}
