// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"strings"

	"github.com/zjcochran/eyrie/internal/analysis"
)

// analysisView is the alert analysis settings as admins see them: never
// the API key.
type analysisView struct {
	Enabled      bool   `json:"enabled"`
	Provider     string `json:"provider"`
	Model        string `json:"model"`
	HasAPIKey    bool   `json:"has_api_key"`
	TimeoutS     int    `json:"timeout_s"`
	DefaultModel string `json:"default_model"`
	// ClaudeCode is where the Claude Code binary is; "" when it is not
	// found, with the reason in ClaudeCodeError.
	ClaudeCode      string `json:"claude_code"`
	ClaudeCodeError string `json:"claude_code_error"`
}

func (s *Server) analysisView(a analysis.Settings) analysisView {
	v := analysisView{Enabled: a.Enabled, Provider: a.Provider, Model: a.Model, HasAPIKey: a.APIKey != "",
		TimeoutS: a.TimeoutS, DefaultModel: analysis.DefaultModel}
	path, err := s.Analyzer.ClaudeCode()
	if err != nil {
		v.ClaudeCodeError = capitalize(err.Error()) + "."
	}
	v.ClaudeCode = path
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

// analysisBody is what saving or testing the settings sends. An empty API
// key keeps the saved one unless clear_api_key is set.
type analysisBody struct {
	Enabled     bool   `json:"enabled"`
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	TimeoutS    int    `json:"timeout_s"`
	APIKey      string `json:"api_key"`
	ClearAPIKey bool   `json:"clear_api_key"`
}

// analysisSettings builds the settings from a body and the saved ones, with
// the API key sealed, and the key in clear. It answers the error itself.
func (s *Server) analysisSettings(w http.ResponseWriter, r *http.Request, b analysisBody) (analysis.Settings, string, bool) {
	old, err := analysis.LoadSettings(r.Context(), s.Store)
	if err != nil {
		s.fail(w, r, err)
		return old, "", false
	}
	next := analysis.Settings{Enabled: b.Enabled, Provider: b.Provider, Model: b.Model, TimeoutS: b.TimeoutS, APIKey: old.APIKey}
	key := strings.TrimSpace(b.APIKey)
	switch {
	case b.ClearAPIKey:
		next.APIKey, key = "", ""
	case key != "":
		if err := analysis.CheckKey(key); err != nil {
			invalid(w, http.StatusUnprocessableEntity, capitalize(err.Error())+".")
			return next, "", false
		}
		if next.APIKey, err = s.Secrets.Seal(key); err != nil {
			s.fail(w, r, err)
			return next, "", false
		}
	case next.APIKey != "":
		if key, err = s.Secrets.Open(next.APIKey); err != nil {
			s.fail(w, r, err)
			return next, "", false
		}
	}
	if err := next.Normalize(); err != nil {
		invalid(w, http.StatusUnprocessableEntity, capitalize(err.Error())+".")
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
	s.Log.Info("alert analysis settings saved", "enabled", next.Enabled, "provider", next.Provider, "model", next.Model,
		"by", sessionFrom(r).User.Username)
	writeJSON(w, http.StatusOK, s.analysisView(next))
}

// testAnalysis asks Claude about a made-up alert with the form as it is,
// without saving. The answer is the analysis or the error.
func (s *Server) testAnalysis(w http.ResponseWriter, r *http.Request) {
	var b analysisBody
	if !readJSON(w, r, &b) {
		return
	}
	b.Enabled = true
	next, key, ok := s.analysisSettings(w, r, b)
	if !ok {
		return
	}
	next.APIKey = key
	text, err := s.Analyzer.Test(r.Context(), next)
	if err != nil {
		writeJSON(w, http.StatusOK, ssoTest{false, capitalize(err.Error()) + "."})
		return
	}
	writeJSON(w, http.StatusOK, ssoTest{true, text})
}
