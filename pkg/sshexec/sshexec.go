package sshexec

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// rootShell runs the script from stdin as root, with sudo only when the SSH
// user is not root already.
const rootShell = `if [ "$(id -u)" -eq 0 ]; then exec bash -s; else exec sudo -n bash -s; fi`

// Client runs commands on a host through the system ssh binary, so aliases,
// jump hosts and keys from ~/.ssh/config keep working.
type Client struct {
	Alias string
}

func New(alias string) *Client {
	return &Client{Alias: alias}
}

// Run pipes script to a root bash on the host and returns its stdout.
// The error carries stderr and the exit code.
func (c *Client) Run(ctx context.Context, script string) (string, error) {
	cmd := exec.CommandContext(ctx, "ssh",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"-o", "LogLevel=ERROR",
		c.Alias, rootShell)
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("ssh %s: %w: %s", c.Alias, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
