// This independent executable exercises finite CLI checks and owned process trees.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version":
		fmt.Println("fixture-cli 1.2.3")
	case "environment":
		json.NewEncoder(os.Stdout).Encode(map[string]bool{"inherited": os.Getenv("DAX_RUNNER_SHOULD_NOT_INHERIT") != "", "allowed": os.Getenv("ONLY_FOR_FIXTURE") == "yes"})
	case "failure":
		fmt.Fprintln(os.Stderr, "synthetic-secret-and-prompt-must-not-appear")
		os.Exit(23)
	case "stdout-overflow":
		fmt.Print(strings.Repeat("z", 1<<20))
	case "stderr-flood":
		for range 4096 {
			fmt.Fprint(os.Stderr, strings.Repeat("s", 4096))
		}
		fmt.Print("complete")
	case "grandchild":
		signal.Ignore(syscall.SIGTERM)
		fmt.Println(os.Getpid())
		for {
			time.Sleep(time.Second)
		}
	case "tree", "leader-exits":
		child := exec.Command(os.Args[0], "grandchild")
		pipe, err := child.StdoutPipe()
		if err != nil || child.Start() != nil {
			os.Exit(3)
		}
		line, err := bufio.NewReader(pipe).ReadString('\n')
		if err != nil {
			os.Exit(4)
		}
		fmt.Print(line)
		if os.Args[1] == "leader-exits" {
			return
		}
		signal.Ignore(syscall.SIGTERM)
		for {
			time.Sleep(time.Second)
		}
	default:
		os.Exit(5)
	}
}
