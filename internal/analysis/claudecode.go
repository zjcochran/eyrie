// SPDX-License-Identifier: AGPL-3.0-or-later

package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// command is the Claude Code binary to run.
func (a *Analyzer) command() string {
	if a.Command != "" {
		return a.Command
	}
	return "claude"
}

// ClaudeCode finds the Claude Code binary, for the settings page.
func (a *Analyzer) ClaudeCode() (string, error) { return exec.LookPath(a.command()) }

// claudeCodeResult is the part of `claude -p --output-format json` read.
type claudeCodeResult struct {
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
}

// claudeCode asks Claude Code, signed in with the admin's own account, in
// print mode: one answer, with no tools, MCP servers, slash commands or
// saved session, in an empty directory so no project settings load.
func (a *Analyzer) claudeCode(ctx context.Context, s Settings, prompt string) (string, error) {
	dir, err := os.MkdirTemp("", "eyrie-claude-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	args := []string{"-p", "--output-format", "json", "--max-turns", "1", "--tools", "", "--strict-mcp-config",
		"--disable-slash-commands", "--no-session-persistence", "--system-prompt", system}
	if s.Model != "" {
		args = append(args, "--model", s.Model)
	}
	run := a.Run
	if run == nil {
		run = runCommand
	}
	out, err := run(ctx, dir, []byte(prompt), a.command(), args...)
	var r claudeCodeResult
	if jerr := json.Unmarshal(bytes.TrimSpace(out), &r); jerr != nil {
		if err != nil {
			return "", fmt.Errorf("claude code: %w", err)
		}
		return "", errors.New("claude code did not answer in JSON")
	}
	if r.IsError || err != nil {
		return "", fmt.Errorf("claude code: %s", Shorten(firstOf(r.Result, err), 300))
	}
	return r.Result, nil
}

func firstOf(result string, err error) string {
	if result != "" || err == nil {
		return result
	}
	return err.Error()
}

// runCommand runs name with os/exec.
func runCommand(ctx context.Context, dir string, stdin []byte, name string, args ...string) ([]byte, error) {
	// The command is EYRIE_CLAUDE_COMMAND, set by whoever runs Eyrie, never
	// by the web app; the arguments are fixed but for the model name,
	// which Settings.Normalize keeps from looking like a flag.
	// nosemgrep
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: the command comes from the environment, not from users
	cmd.Dir, cmd.Stdin, cmd.WaitDelay = dir, bytes.NewReader(stdin), 5*time.Second
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil && stderr.Len() > 0 {
		err = fmt.Errorf("%w: %s", err, strings.TrimSpace(Shorten(stderr.String(), 300)))
	}
	return stdout.Bytes(), err
}
