// SPDX-License-Identifier: AGPL-3.0-or-later

// Package analysis asks a language model why a monitor went down, so its
// down alert can carry the answer. It is off until an admin turns it on,
// and uses one of:
//
//   - Claude, through the Claude API with an Anthropic API key;
//   - Claude with the admin's Claude subscription, through the unmodified
//     Claude Code binary, which Eyrie downloads from Anthropic when
//     EYRIE_CLAUDE_CODE is true (ClaudeCode) and the admin signs in with
//     CLAUDE_CODE_OAUTH_TOKEN; Eyrie never reads that token;
//   - a model on a server the admin runs (Ollama, LM Studio, llama.cpp,
//     vLLM), through the OpenAI-compatible chat completions API at its
//     URL, with an optional API key.
//
// API keys are stored sealed like other secrets.
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
	"net/url"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/zjcochran/eyrie/internal/store"
)

// Providers: which model answers.
const (
	ProviderAPI        = "api"         // Claude, through the Claude API
	ProviderClaudeCode = "claude-code" // Claude, through Claude Code
	ProviderLocal      = "local"       // an OpenAI-compatible server
)

// DefaultModel is the Claude API model used when none is set.
const DefaultModel = "claude-opus-5-5"

// SettingKey is the settings row that holds Settings as JSON.
const SettingKey = "analysis"

// MaxLength is the longest analysis, in characters, put in a message;
// the model is asked for less.
const MaxLength = 800

// Settings are the alert analysis settings.
type Settings struct {
	Enabled  bool   `json:"enabled"`
	Provider string `json:"provider"`
	// Model is a model name; "" is DefaultModel for the Claude API and the
	// account's default for Claude Code, and required for a local server.
	Model  string `json:"model"`
	APIKey string `json:"api_key"` // sealed; the Anthropic API key
	// BaseURL is the local server's OpenAI-compatible API, such as
	// http://192.0.2.20:11434/v1, without a trailing slash.
	BaseURL  string `json:"base_url"`
	LocalKey string `json:"local_api_key"` // sealed; "" when the server needs none
	TimeoutS int    `json:"timeout_s"`
}

// DefaultSettings are off, the Claude API and a 60 second timeout.
func DefaultSettings() Settings { return Settings{Provider: ProviderAPI, TimeoutS: 60} }

// Normalize checks the settings. Errors are meant for people.
func (s *Settings) Normalize() error {
	s.Model = strings.TrimSpace(s.Model)
	s.BaseURL = strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
	if s.Provider != ProviderAPI && s.Provider != ProviderClaudeCode && s.Provider != ProviderLocal {
		return fmt.Errorf("unknown provider %q", s.Provider)
	}
	if s.Provider != ProviderLocal {
		s.BaseURL = "" // only your own server has one
	}
	if s.TimeoutS < 5 || s.TimeoutS > 300 {
		return errors.New("the timeout is from 5 to 300 seconds")
	}
	if strings.ContainsAny(s.Model, " \t\r\n") || strings.HasPrefix(s.Model, "-") || len(s.Model) > 100 {
		return fmt.Errorf("%q is not a model name", s.Model)
	}
	if s.BaseURL != "" && !serverURL(s.BaseURL) {
		return fmt.Errorf("%q is not a server URL such as http://192.0.2.20:11434/v1", s.BaseURL)
	}
	return s.ready()
}

// ready refuses turning analysis on without what the provider needs.
func (s *Settings) ready() error {
	if s.Enabled && s.Provider == ProviderAPI && s.APIKey == "" {
		return errors.New("the Claude API needs an API key")
	}
	if s.Enabled && s.Provider == ProviderLocal && (s.BaseURL == "" || s.Model == "") {
		return errors.New("a local model needs the server URL and the model name")
	}
	return nil
}

// serverURL says whether u is an http or https URL with a host, and
// nothing that does not belong in a base URL.
func serverURL(u string) bool {
	p, err := url.Parse(u)
	return err == nil && (p.Scheme == "http" || p.Scheme == "https") && p.Host != "" && p.User == nil &&
		p.RawQuery == "" && p.Fragment == ""
}

// Key is the sealed API key of the settings' provider.
func (s *Settings) Key() *string {
	if s.Provider == ProviderLocal {
		return &s.LocalKey
	}
	return &s.APIKey
}

// CheckKey refuses what is not an API key. Errors are meant for people.
func CheckKey(key string) error {
	if strings.HasPrefix(key, "sk-ant-oat") {
		return errors.New("that is a Claude subscription token, which Anthropic allows only in its own apps; " +
			"to use your subscription, set it as CLAUDE_CODE_OAUTH_TOKEN with EYRIE_CLAUDE_CODE=true and choose Claude Code")
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

// Analyzer asks a model about alerts. Set its fields, then SetSettings.
type Analyzer struct {
	Store   *store.Store
	Secrets Sealer
	Log     *slog.Logger
	Client  *http.Client // nil: http.DefaultClient; the timeout comes from the settings
	APIURL  string       // "": https://api.anthropic.com
	Version string       // sent in the User-Agent
	// Claude is Claude Code, when EYRIE_CLAUDE_CODE is true; nil otherwise.
	Claude *ClaudeCode
	Run    Runner // nil: os/exec
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

// Analyze asks the model about a monitor that went down with beat b. It
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
	a.Log.Info("asking the model to analyse the alert", "monitor", m.Name, "provider", s.Provider, "model", s.Model)
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

// Test asks the model about a made-up alert with settings s, whose API
// keys are in clear, and returns the answer.
func (a *Analyzer) Test(ctx context.Context, s Settings) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout(s))
	defer cancel()
	text, err := a.askKey(ctx, s, *s.Key(), testPrompt)
	if ctx.Err() != nil {
		return "", fmt.Errorf("no answer within %d s", s.TimeoutS)
	}
	return text, err
}

// ask sends the prompt with the saved settings.
func (a *Analyzer) ask(ctx context.Context, s Settings, prompt string) (string, error) {
	key := *s.Key()
	if key != "" {
		var err error
		if key, err = a.Secrets.Open(key); err != nil {
			return "", err
		}
	}
	return a.askKey(ctx, s, key, prompt)
}

func (a *Analyzer) askKey(ctx context.Context, s Settings, key, prompt string) (string, error) {
	var text string
	var err error
	switch s.Provider {
	case ProviderLocal:
		text, err = a.chat(ctx, s, key, prompt)
	case ProviderClaudeCode:
		text, err = a.claudeCode(ctx, s, prompt)
	default:
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
