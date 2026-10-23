// SPDX-License-Identifier: AGPL-3.0-or-later

// Package analysis asks Claude why a monitor went down, so its down alert
// can carry the answer. It is off until an admin turns it on, and reaches
// Claude in one of two ways:
//
//   - the Claude API with an Anthropic API key, stored sealed like other
//     secrets;
//   - Claude Code, the unmodified binary installed where Eyrie runs and
//     signed in by the admin with their own Claude subscription. Eyrie
//     never sees or stores that sign-in: Anthropic's terms allow
//     subscription credentials only in its own apps.
//
// An alert never waits longer than the timeout: without an answer by then
// it goes out without one.
package analysis

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/zjcochran/eyrie/internal/store"
)

// Providers: how Eyrie reaches Claude.
const (
	ProviderAPI        = "api"
	ProviderClaudeCode = "claude-code"
)

// DefaultModel is the Claude API model used when none is set.
const DefaultModel = "claude-opus-5-5"

// SettingKey is the settings row that holds Settings as JSON.
const SettingKey = "analysis"

// MaxLength is the longest analysis, in characters, put in a message;
// Claude is asked for less.
const MaxLength = 800

// Settings are the alert analysis settings.
type Settings struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"`
	// Model is a model ID or, for Claude Code, an alias such as opus; ""
	// is DefaultModel for the API and the account's default for Claude
	// Code.
	Model    string `json:"model"`
	APIKey   string `json:"api_key"` // sealed
	TimeoutS int    `json:"timeout_s"`
}

// DefaultSettings are off, the Claude API and a 60 second timeout.
func DefaultSettings() Settings { return Settings{Provider: ProviderAPI, TimeoutS: 60} }

// Normalize checks the settings. Errors are meant for people.
func (s *Settings) Normalize() error {
	s.Model = strings.TrimSpace(s.Model)
	if s.Provider != ProviderAPI && s.Provider != ProviderClaudeCode {
		return fmt.Errorf("unknown provider %q", s.Provider)
	}
	if s.TimeoutS < 5 || s.TimeoutS > 300 {
		return errors.New("the timeout is from 5 to 300 seconds")
	}
	if strings.ContainsAny(s.Model, " \t\r\n") || strings.HasPrefix(s.Model, "-") || len(s.Model) > 100 {
		return fmt.Errorf("%q is not a model name", s.Model)
	}
	if s.Enabled && s.Provider == ProviderAPI && s.APIKey == "" {
		return errors.New("the Claude API needs an API key")
	}
	return nil
}

// CheckKey refuses what is not an API key. Errors are meant for people.
func CheckKey(key string) error {
	if strings.HasPrefix(key, "sk-ant-oat") {
		return errors.New("that is a Claude subscription token, which Anthropic allows only in its own apps; " +
			"choose Claude Code to use your subscription, or create an API key in the Claude Console")
	}
	if strings.ContainsAny(key, " \t\r\n") {
		return errors.New("the API key cannot contain spaces")
	}
	return nil
}

// Sealer seals and opens stored secrets.
type Sealer interface {
	Seal(plain string) (string, error)
	Open(sealed string) (string, error)
}

// LoadSettings reads the settings, with their defaults.
func LoadSettings(ctx context.Context, st *store.Store) (Settings, error) {
	s := DefaultSettings()
	_, err := st.GetSetting(ctx, SettingKey, &s)
	return s, err
}

// Runner runs a command with stdin in dir and returns its standard output.
type Runner func(ctx context.Context, dir string, stdin []byte, name string, args ...string) ([]byte, error)

// Analyzer asks Claude about alerts. Set its fields, then SetSettings.
type Analyzer struct {
	Store   *store.Store
	Secrets Sealer
	Log     *slog.Logger
	Client  *http.Client // nil: http.DefaultClient; the timeout comes from the settings
	APIURL  string       // "": https://api.anthropic.com
	Version string       // sent in the User-Agent
	// Command is the Claude Code binary: EYRIE_CLAUDE_COMMAND, "claude"
	// by default.
	Command string
	Run     Runner // nil: os/exec
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// second is how long a second of the timeout lasts: shorter in tests.
	second time.Duration

	settings atomic.Pointer[Settings]
	// running counts analyses in progress, at most maxRunning at once.
	running    atomic.Int32
	ok, failed atomic.Uint64
}

// maxRunning bounds analyses at once, so an outage that takes down many
// monitors does not start as many requests.
const maxRunning = 4

// SetSettings makes saved settings take effect.
func (a *Analyzer) SetSettings(s Settings) { a.settings.Store(&s) }

// Settings are the settings in effect.
func (a *Analyzer) Settings() Settings {
	if s := a.settings.Load(); s != nil {
		return *s
	}
	return DefaultSettings()
}

// Counts are the analyses that worked and that failed or timed out.
func (a *Analyzer) Counts() (ok, failed uint64) { return a.ok.Load(), a.failed.Load() }

func (a *Analyzer) timeout(s Settings) time.Duration {
	if a.second != 0 {
		return time.Duration(s.TimeoutS) * a.second
	}
	return time.Duration(s.TimeoutS) * time.Second
}

func (a *Analyzer) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

// Analyze asks Claude about a monitor that went down with beat b. It
// returns "" when analysis is off, busy, fails or takes longer than the
// timeout; failures are logged.
func (a *Analyzer) Analyze(m store.Monitor, b store.Beat) string {
	s := a.Settings()
	if !s.Enabled {
		return ""
	}
	if a.running.Add(1) > maxRunning {
		a.running.Add(-1)
		a.failed.Add(1)
		a.Log.Warn("alert analysis skipped: too many at once", "monitor", m.Name)
		return ""
	}
	defer a.running.Add(-1)
	ctx, cancel := context.WithTimeout(context.Background(), a.timeout(s))
	defer cancel()
	prompt, err := a.prompt(ctx, m, b)
	if err == nil {
		var text string
		if text, err = a.ask(ctx, s, prompt); err == nil {
			a.ok.Add(1)
			return text
		}
	}
	a.failed.Add(1)
	if ctx.Err() != nil {
		err = fmt.Errorf("no answer within %d s", s.TimeoutS)
	}
	a.Log.Warn("alert analysis failed", "monitor", m.Name, "provider", s.Provider, "err", err)
	return ""
}

// Test asks Claude about a made-up alert with settings s, whose APIKey is
// in clear, and returns the answer.
func (a *Analyzer) Test(ctx context.Context, s Settings) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout(s))
	defer cancel()
	text, err := a.askKey(ctx, s, s.APIKey, testPrompt)
	if ctx.Err() != nil {
		return "", fmt.Errorf("no answer within %d s", s.TimeoutS)
	}
	return text, err
}

// ask sends the prompt with the saved settings.
func (a *Analyzer) ask(ctx context.Context, s Settings, prompt string) (string, error) {
	key := ""
	if s.Provider == ProviderAPI {
		var err error
		if key, err = a.Secrets.Open(s.APIKey); err != nil {
			return "", err
		}
	}
	return a.askKey(ctx, s, key, prompt)
}

func (a *Analyzer) askKey(ctx context.Context, s Settings, key, prompt string) (string, error) {
	var text string
	var err error
	if s.Provider == ProviderClaudeCode {
		text, err = a.claudeCode(ctx, s, prompt)
	} else {
		text, err = a.messages(ctx, s, key, prompt)
	}
	if err != nil {
		return "", err
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", errors.New("the answer was empty")
	}
	return Shorten(text, MaxLength), nil
}

// Shorten cuts s to at most n characters, ending it with "…" when cut.
func Shorten(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}
