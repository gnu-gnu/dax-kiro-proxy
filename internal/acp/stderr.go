package acp

import (
	"bufio"
	"errors"
	"regexp"
)

var credentialHint = regexp.MustCompile(`(?i)(authorization|bearer\s|api[-_]?key|access[-_]?token|refresh[-_]?token|client[-_]?secret|password|sk-[a-z0-9_-]+|eyJ[a-z0-9_-]+\.)`)

func retainableStderr(line []byte, truncated bool) string {
	if truncated {
		return "[stderr line omitted: size limit]"
	}
	if credentialHint.Match(line) {
		return "[credential-bearing stderr omitted]"
	}
	return string(line)
}
func (c *Client) stderrLoop() {
	defer close(c.stderrDone)
	r := bufio.NewReaderSize(c.stderr, 4096)
	var line []byte
	truncated := false
	for {
		fragment, err := r.ReadSlice('\n')
		if len(fragment) > 4096-len(line) {
			line = append(line, fragment[:4096-len(line)]...)
			truncated = true
		} else {
			line = append(line, fragment...)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if len(line) > 0 {
			if c.config.Auth != nil && c.config.Auth.Stderr(line) {
				c.retire(ErrAuthentication)
			}
			retained := retainableStderr(line, truncated)
			c.mu.Lock()
			c.stderrTail = append(c.stderrTail, retained)
			c.stderrBytes += len(retained)
			if truncated {
				c.truncated++
			}
			for len(c.stderrTail) > 64 || c.stderrBytes > 64<<10 {
				c.stderrBytes -= len(c.stderrTail[0])
				c.stderrTail[0] = ""
				c.stderrTail = c.stderrTail[1:]
			}
			c.mu.Unlock()
		}
		line = nil
		truncated = false
		if err != nil {
			return
		}
	}
}
func (c *Client) Diagnostics() Diagnostics {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Diagnostics{len(c.stderrTail), c.stderrBytes, c.truncated}
}
