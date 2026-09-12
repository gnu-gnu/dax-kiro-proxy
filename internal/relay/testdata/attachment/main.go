// Independent private-protocol peer. It performs no tool effect and imports no product package.
package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "owner" {
		fmt.Println("owner")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return
	}
	if len(os.Args) != 3 {
		os.Exit(30)
	}
	file, err := os.Open(os.Args[1])
	if err != nil {
		os.Exit(31)
	}
	var config struct{ Socket, Owner, Secret string }
	err = json.NewDecoder(io.LimitReader(file, 2<<20)).Decode(&config)
	_ = file.Close()
	if err != nil {
		os.Exit(32)
	}
	conn, err := net.DialTimeout("unix", config.Socket, time.Second)
	if err != nil {
		os.Exit(33)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	mode := os.Args[2]
	request := map[string]any{"version": 1, "operation": "attach", "owner": config.Owner, "secret": config.Secret}
	if mode == "wrong" {
		request["secret"] = "independent-wrong-secret"
	}
	if mode == "extra" {
		request["pid"] = os.Getppid()
	}
	write(conn, request)
	if mode == "delayed" || mode == "delayed-stall" {
		fmt.Println("attaching")
	}
	response, ok := read(conn)
	if !ok {
		fmt.Println("rejected")
		return
	}
	var group int
	if string(response["version"]) != "1" || string(response["operation"]) != `"attach"` || json.Unmarshal(response["group"], &group) != nil || group <= 1 {
		os.Exit(34)
	}
	if mode == "stall" || mode == "delayed-stall" {
		_, _ = read(conn)
		fmt.Println("rejected")
		return
	}
	if mode != "lie" && syscall.Setpgid(0, group) != nil {
		os.Exit(35)
	}
	operation := "joined"
	if mode == "bad-ack" {
		operation = "invented-ack"
	}
	write(conn, map[string]any{"version": 1, "operation": operation})
	response, ok = read(conn)
	if !ok {
		fmt.Println("rejected")
		return
	}
	if string(response["version"]) != "1" || string(response["operation"]) != `"ready"` {
		os.Exit(36)
	}
	fmt.Println("ready")
	if mode == "lost" {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	var one [1]byte
	_, _ = conn.Read(one[:])
	if mode == "linger" {
		_, _ = io.Copy(io.Discard, os.Stdin)
	}
}

func write(conn net.Conn, value any) {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > 4096 || binary.Write(conn, binary.BigEndian, uint32(len(raw))) != nil {
		os.Exit(37)
	}
	if _, err := conn.Write(raw); err != nil {
		os.Exit(38)
	}
}

func read(conn net.Conn) (map[string]json.RawMessage, bool) {
	var size uint32
	if binary.Read(conn, binary.BigEndian, &size) != nil || size > 4096 || size < 2 {
		return nil, false
	}
	raw := make([]byte, size)
	if _, err := io.ReadFull(conn, raw); err != nil {
		return nil, false
	}
	var value map[string]json.RawMessage
	err := json.Unmarshal(raw, &value)
	return value, err == nil && value != nil
}
