// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/zjcochran/eyrie/internal/analysis"
	"github.com/zjcochran/eyrie/internal/notify"
)

// analysisView is the alert analysis settings as admins see them: never
// the API keys.
type analysisView struct {
	Enabled        bool   `json:"enabled"`
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	HasAPIKey      bool   `json:"has_api_key"`
	BaseURL        string `json:"base_url"`
	HasLocalAPIKey bool   `json:"has_local_api_key"`
	TimeoutS       int    `json:"timeout_s"`
	DefaultModel   string `json:"default_model"`
	// ClaudeCode is the Claude Code download; null when EYRIE_CLAUDE_CODE
	// is not true.
	ClaudeCode *analysis.ClaudeCodeStatus `json:"claude_code"`
}

func (s *Server) analysisView(a analysis.Settings) analysisView {
	v := analysisView{Enabled: a.Enabled, Provider: a.Provider, Model: a.Model, HasAPIKey: a.APIKey != "",
		BaseURL: a.BaseURL, HasLocalAPIKey: a.LocalKey != "", TimeoutS: a.TimeoutS, DefaultModel: analysis.DefaultModel}
	if s.Analyzer.Claude != nil {
		st := s.Analyzer.Claude.Status()
		v.ClaudeCode = &st
	}
	return v
}

func (s *Server) getAnalysis(w http.ResponseWriter, r *http.Request) {
	a, err := analysis.LoadSettings(r.Context(), s.Store)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.analysisView(a))
}

// analysisBody is what saving or testing the settings sends. api_key and
// clear_api_key are the chosen provider's key: an empty one keeps the
// saved key unless clear_api_key is set. The other provider's key is kept.
type analysisBody struct {
	Enabled     bool   `json:"enabled"`
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	BaseURL     string `json:"base_url"`
	TimeoutS    int    `json:"timeout_s"`
	APIKey      string `json:"api_key"`
	ClearAPIKey bool   `json:"clear_api_key"`
	// ChannelID, when testing, is a saved channel the made-up alert goes
	// to, followed by the answer; 0 only shows the answer.
	ChannelID int64 `json:"channel_id"`
}

// analysisSettings builds the settings from a body and the saved ones, with
// the API keys sealed, and the chosen provider's key in clear. It answers
// the error itself.
func (s *Server) analysisSettings(w http.ResponseWriter, r *http.Request, b analysisBody) (analysis.Settings, string, bool) {
	old, err := analysis.LoadSettings(r.Context(), s.Store)
	if err != nil {
		s.fail(w, r, err)
		return old, "", false
	}
	next := analysis.Settings{Enabled: b.Enabled, Provider: b.Provider, Model: b.Model, BaseURL: b.BaseURL,
		TimeoutS: b.TimeoutS, APIKey: old.APIKey, LocalKey: old.LocalKey}
	sealed := next.Key()
	key := strings.TrimSpace(b.APIKey)
	switch {
	case b.ClearAPIKey:
		*sealed, key = "", ""
	case key != "":
		if err := analysis.CheckKey(key); err != nil {
			invalid(w, http.StatusUnprocessableEntity, capitalize(err.Error())+".")
			return next, "", false
		}
		if *sealed, err = s.Secrets.Seal(key); err != nil {
			s.fail(w, r, err)
			return next, "", false
		}
	case *sealed != "":
		if key, err = s.Secrets.Open(*sealed); err != nil {
			s.fail(w, r, err)
			return next, "", false
		}
	}
	if err := next.Normalize(); err != nil {
		invalid(w, http.StatusUnprocessableEntity, capitalize(err.Error())+".")
		return next, "", false
	}
	if next.Provider == analysis.ProviderClaudeCode && s.Analyzer.Claude == nil {
		invalid(w, http.StatusUnprocessableEntity, "Claude Code is off: set EYRIE_CLAUDE_CODE=true in Eyrie's environment and restart it.")
		return next, "", false
	}
	return next, key, true
}

func (s *Server) saveAnalysis(w http.ResponseWriter, r *http.Request) {
	var b analysisBody
	if !readJSON(w, r, &b) {
		return
	}
	next, _, ok := s.analysisSettings(w, r, b)
	if !ok {
		return
	}
	if err := s.Store.SetSetting(r.Context(), analysis.SettingKey, next, s.now()); err != nil {
		s.fail(w, r, err)
		return
	}
	s.Analyzer.SetSettings(next)
	s.audit(r, "alert analysis settings saved", "enabled", next.Enabled, "provider", next.Provider, "model", next.Model,
		"base_url", next.BaseURL, "by", sessionFrom(r).User.Username)
	writeJSON(w, http.StatusOK, s.analysisView(next))
}

// testAnalysis asks the model about a made-up alert with the form as it is,
// without saving. With a channel, the alert goes to it first, and the
// answer follows in its own message, as on a real alert. The answer is the
// analysis or the error.
func (s *Server) testAnalysis(w http.ResponseWriter, r *http.Request) {
	var b analysisBody
	if !readJSON(w, r, &b) {
		return
	}
	var c notify.Channel
	if b.ChannelID != 0 {
		sc, err := s.Store.ChannelByID(r.Context(), b.ChannelID)
		if err != nil {
			s.storeError(w, r, err)
			return
		}
		if c, err = notify.FromStore(sc, s.Secrets); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	b.Enabled = true
	next, key, ok := s.analysisSettings(w, r, b)
	if !ok {
		return
	}
	*next.Key() = key
	var sendErr error
	if c.ID != 0 {
		sendErr = s.Notifier.TestAlert(r.Context(), c)
	}
	text, err := s.Analyzer.Test(r.Context(), next)
	if err != nil {
		writeJSON(w, http.StatusOK, ssoTest{false, capitalize(err.Error()) + "."})
		return
	}
	if c.ID == 0 {
		writeJSON(w, http.StatusOK, ssoTest{true, text})
		return
	}
	if sendErr == nil {
		sendErr = s.Notifier.TestAnalysis(r.Context(), c, text)
	}
	if sendErr != nil {
		writeJSON(w, http.StatusOK, ssoTest{false, fmt.Sprintf("%s\n\nSending to %s failed: %s.", text, c.Name, sendErr)})
		return
	}
	writeJSON(w, http.StatusOK, ssoTest{true, fmt.Sprintf("%s\n\nSent the alert, then the analysis, to %s.", text, c.Name)})
}

// modelsAnswer is the provider's models, or why they could not be listed.
type modelsAnswer struct {
	Models []string `json:"models"`
	Error  string   `json:"error,omitempty"`
}

// analysisModels lists the models of the provider in the form as it is,
// saved or not, for the settings page to offer.
func (s *Server) analysisModels(w http.ResponseWriter, r *http.Request) {
	var b analysisBody
	if !readJSON(w, r, &b) {
		return
	}
	b.Enabled = false
	next, key, ok := s.analysisSettings(w, r, b)
	if !ok {
		return
	}
	*next.Key() = key
	models, err := s.Analyzer.Models(r.Context(), next)
	if err != nil {
		writeJSON(w, http.StatusOK, modelsAnswer{Models: []string{}, Error: capitalize(err.Error()) + "."})
		return
	}
	writeJSON(w, http.StatusOK, modelsAnswer{Models: models})
}
