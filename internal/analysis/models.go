// SPDX-License-Identifier: AGPL-3.0-or-later

package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"
)

// modelsTimeout bounds listing models, for the settings page.
const modelsTimeout = 10 * time.Second

type modelList struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
	Error json.RawMessage `json:"error"`
}

// Models lists the models the provider of settings s offers, whose API
// keys are in clear: the Claude API's newest first (GET /v1/models),
// Claude Code's aliases, a local server's by name (GET <base URL>/models).
func (a *Analyzer) Models(ctx context.Context, s Settings) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, modelsTimeout)
	defer cancel()
	var resp answer
	var err error
	who := "the Claude API"
	if s.Provider == ProviderClaudeCode {
		// Claude Code takes these aliases for the latest of each model.
		return []string{"opus", "sonnet", "haiku"}, nil
	}
	if s.Provider == ProviderLocal {
		if s.BaseURL == "" {
			return nil, errors.New("enter the server URL first")
		}
		who = "the model server"
		h := http.Header{}
		if s.LocalKey != "" {
			h.Set("Authorization", "Bearer "+s.LocalKey)
		}
		resp, err = a.get(ctx, s.BaseURL+"/models", h)
	} else {
		if s.APIKey == "" {
			return nil, errors.New("enter the API key first")
		}
		resp, err = a.get(ctx, a.apiURL()+"/v1/models?limit=1000", anthropicHeaders(s.APIKey))
	}
	if err != nil {
		return nil, err
	}
	var r modelList
	if err := json.Unmarshal(resp.Body, &r); err != nil {
		return nil, fmt.Errorf("%s answered %s", who, resp.Status)
	}
	if msg := errorMessage(r.Error); msg != "" {
		return nil, fmt.Errorf("%s answered %s: %s", who, resp.Status, msg)
	}
	if resp.Code >= 300 {
		return nil, fmt.Errorf("%s answered %s", who, resp.Status)
	}
	ids := []string{}
	for _, m := range r.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	if s.Provider == ProviderLocal {
		slices.Sort(ids)
	}
	return ids, nil
}

func (a *Analyzer) get(ctx context.Context, url string, h http.Header) (answer, error) {
	return a.do(ctx, http.MethodGet, url, h, nil)
}
