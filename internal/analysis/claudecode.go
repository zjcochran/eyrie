// SPDX-License-Identifier: AGPL-3.0-or-later

package analysis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// ClaudeCodeReleases is where Anthropic publishes Claude Code, as its own
// installer (https://claude.ai/install.sh) downloads it.
const ClaudeCodeReleases = "https://downloads.claude.ai/claude-code-releases"

// ClaudeCode downloads the unmodified Claude Code binary from Anthropic
// into Dir and keeps it at the stable release, so analysis can use a
// Claude subscription: EYRIE_CLAUDE_CODE=true. The admin signs it in with
// CLAUDE_CODE_OAUTH_TOKEN in Eyrie's environment; Eyrie never reads it.
type ClaudeCode struct {
	Dir     string // <data dir>/claude-code
	BaseURL string // "": ClaudeCodeReleases
	Client  *http.Client
	Log     *slog.Logger
	// Platform is the release's platform, such as linux-x64; "" detects it.
	Platform string

	mu      sync.Mutex
	status  ClaudeCodeStatus
	path    string
	running sync.Mutex // one install at a time
}

// ClaudeCodeStatus is where the download stands, for the settings page.
type ClaudeCodeStatus struct {
	State   string `json:"state"` // "installing", "ready" or "failed"
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Status says where the download stands.
func (c *ClaudeCode) Status() ClaudeCodeStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.status.State == "" {
		return ClaudeCodeStatus{State: "installing"}
	}
	return c.status
}

// Path is the binary, once downloaded.
func (c *ClaudeCode) Path() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.path != "":
		return c.path, nil
	case c.status.State == "failed":
		return "", errors.New("the Claude Code download failed: " + c.status.Error)
	}
	return "", errors.New("the Claude Code download is not finished yet")
}

func (c *ClaudeCode) set(s ClaudeCodeStatus, path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status = s
	if path != "" {
		c.path = path
	}
}

// Run installs Claude Code, then checks for a new stable release every
// day (every hour after a failure), until ctx ends.
func (c *ClaudeCode) Run(ctx context.Context) {
	for {
		wait := 24 * time.Hour
		if err := c.Install(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			c.Log.Error("cannot download Claude Code", "err", err)
			wait = time.Hour
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

var releaseVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.]+)?$`)

// Install makes sure the stable release is in Dir/<version>/claude,
// downloading it if needed, and removes other versions.
func (c *ClaudeCode) Install(ctx context.Context) error {
	c.running.Lock()
	defer c.running.Unlock()
	v, path, err := c.install(ctx)
	if err != nil {
		c.mu.Lock()
		failed := c.path == "" // a working older version stays ready
		c.mu.Unlock()
		if failed {
			c.set(ClaudeCodeStatus{State: "failed", Error: err.Error()}, "")
		}
		return err
	}
	c.set(ClaudeCodeStatus{State: "ready", Version: v}, path)
	return nil
}

func (c *ClaudeCode) install(ctx context.Context) (string, string, error) {
	platform, err := c.platform()
	if err != nil {
		return "", "", err
	}
	b, err := c.fetch(ctx, "/stable", 100)
	if err != nil {
		return "", "", err
	}
	v := strings.TrimSpace(string(b))
	if !releaseVersion.MatchString(v) {
		return "", "", fmt.Errorf("%q is not a Claude Code version", Shorten(v, 40))
	}
	path := filepath.Join(c.Dir, v, "claude")
	if _, err := os.Stat(path); err != nil {
		if err := c.download(ctx, v, platform, path); err != nil {
			return "", "", err
		}
		c.Log.Info("Claude Code downloaded", "version", v, "platform", platform)
	}
	c.prune(v)
	return v, path, nil
}

// download fetches the binary for version v, checks its size and SHA-256
// against the release's manifest, and moves it to path.
func (c *ClaudeCode) download(ctx context.Context, v, platform, path string) error {
	b, err := c.fetch(ctx, "/"+v+"/manifest.json", 1<<20)
	if err != nil {
		return err
	}
	var manifest struct {
		Platforms map[string]struct {
			Checksum string `json:"checksum"`
			Size     int64  `json:"size"`
		} `json:"platforms"`
	}
	if err := json.Unmarshal(b, &manifest); err != nil {
		return fmt.Errorf("the manifest of Claude Code %s: %w", v, err)
	}
	want, ok := manifest.Platforms[platform]
	if !ok || len(want.Checksum) != 64 || want.Size <= 0 {
		return fmt.Errorf("release %s of Claude Code has no build for %s", v, platform)
	}
	resp, err := c.get(ctx, "/"+v+"/"+platform+"/claude")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "claude-*.part")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, want.Size+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("downloading Claude Code %s: %w", v, err)
	}
	if n != want.Size || hex.EncodeToString(h.Sum(nil)) != want.Checksum {
		return fmt.Errorf("the download of Claude Code %s for %s does not match its manifest's size and SHA-256", v, platform)
	}
	if err := os.Chmod(f.Name(), 0o700); err != nil { //nolint:gosec // G302: a program must be executable, by Eyrie's user only
		return err
	}
	return os.Rename(f.Name(), path)
}

// prune removes the versions other than keep.
func (c *ClaudeCode) prune(keep string) {
	entries, _ := os.ReadDir(c.Dir)
	for _, e := range entries {
		if e.IsDir() && e.Name() != keep && releaseVersion.MatchString(e.Name()) {
			if err := os.RemoveAll(filepath.Join(c.Dir, e.Name())); err != nil {
				c.Log.Warn("cannot remove an old Claude Code", "version", e.Name(), "err", err)
			}
		}
	}
}

func (c *ClaudeCode) get(ctx context.Context, path string) (*http.Response, error) {
	base := c.BaseURL
	if base == "" {
		base = ClaudeCodeReleases
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return nil, err
	}
	client := c.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("%s answered %s", base+path, resp.Status)
	}
	return resp, nil
}

// fetch reads a small file of the releases, at most limit bytes.
func (c *ClaudeCode) fetch(ctx context.Context, path string, limit int64) ([]byte, error) {
	resp, err := c.get(ctx, path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// platform is the release platform Eyrie runs on, as the installer names
// them: linux-x64, linux-arm64-musl, darwin-arm64.
func (c *ClaudeCode) platform() (string, error) {
	if c.Platform != "" {
		return c.Platform, nil
	}
	return platformOf(runtime.GOOS, runtime.GOARCH, musl)
}

// musl says whether the system's C library is musl (Alpine).
func musl() bool {
	for _, f := range []string{"/lib/ld-musl-x86_64.so.1", "/lib/ld-musl-aarch64.so.1"} {
		if _, err := os.Stat(f); err == nil {
			return true
		}
	}
	return false
}

func platformOf(goos, goarch string, musl func() bool) (string, error) {
	arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[goarch]
	if arch == "" || (goos != "linux" && goos != "darwin") {
		return "", fmt.Errorf("there is no Claude Code for %s/%s", goos, goarch)
	}
	p := goos + "-" + arch
	if goos == "linux" && musl() {
		p += "-musl"
	}
	return p, nil
}

// claudeCodeResult is the part of `claude -p --output-format json` read.
type claudeCodeResult struct {
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
}

// claudeCode asks Claude Code, signed in with the admin's own account, in
// print mode: one answer, with no tools, MCP servers, slash commands or
// saved session, in an empty directory so no project settings load. Its
// settings live in Dir/config, and it does not update itself: Run does.
func (a *Analyzer) claudeCode(ctx context.Context, s Settings, prompt string) (string, error) {
	if a.Claude == nil {
		return "", errors.New("claude code is off: set EYRIE_CLAUDE_CODE=true")
	}
	bin, err := a.Claude.Path()
	if err != nil {
		return "", err
	}
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
	env := []string{"CLAUDE_CONFIG_DIR=" + filepath.Join(a.Claude.Dir, "config"), "DISABLE_AUTOUPDATER=1"}
	run := a.Run
	if run == nil {
		run = runCommand
	}
	out, err := run(ctx, dir, env, []byte(prompt), bin, args...)
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

// Runner runs a command in dir, with env added to Eyrie's environment and
// stdin, and returns its standard output.
type Runner func(ctx context.Context, dir string, env []string, stdin []byte, name string, args ...string) ([]byte, error)

// runCommand runs name with os/exec.
func runCommand(ctx context.Context, dir string, env []string, stdin []byte, name string, args ...string) ([]byte, error) {
	// The command is the binary Eyrie downloaded and checked; the arguments
	// are fixed but for the model name, which Settings.Normalize keeps from
	// looking like a flag.
	// nosemgrep
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // G204: Eyrie's own download, not a user's command
	cmd.Dir, cmd.Stdin, cmd.WaitDelay = dir, bytes.NewReader(stdin), 5*time.Second
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil && stderr.Len() > 0 {
		err = fmt.Errorf("%w: %s", err, strings.TrimSpace(Shorten(stderr.String(), 300)))
	}
	return stdout.Bytes(), err
}
