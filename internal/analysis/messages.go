// SPDX-License-Identifier: AGPL-3.0-or-later

package analysis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
)

// fallbackModels are the models that take "fallbacks": "default": a
// request their safety classifiers decline is answered by the model
// Anthropic recommends instead, rather than refused.
var fallbackModels = []string{"claude-opus-5-5", "claude-opus-5", "claude-fable-5-1", "claude-sonnet-5-5"}

type messagesRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system"`
	Messages  []message `json:"messages"`
	Fallbacks string    `json:"fallbacks,omitempty"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type messagesResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// messages asks the Claude API (POST /v1/messages) with an API key.
func (a *Analyzer) messages(ctx context.Context, s Settings, key, prompt string) (string, error) {
	model := s.Model
	if model == "" {
		model = DefaultModel
	}
	body := messagesRequest{Model: model, MaxTokens: 16000, System: system, Messages: []message{{"user", prompt}}}
	if slices.Contains(fallbackModels, model) {
		body.Fallbacks = "default"
	}
	b, _ := json.Marshal(body)
	base := a.APIURL
	if base == "" {
		base = "https://api.anthropic.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/v1/messages", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", key)
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("User-Agent", "Eyrie/"+a.Version)
	if body.Fallbacks != "" {
		req.Header.Set("Anthropic-Beta", "server-side-fallback-2026-07-01")
	}
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var r messagesResponse
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("the Claude API answered %s", resp.Status)
	}
	if r.Error != nil {
		return "", fmt.Errorf("the Claude API answered %s: %s", resp.Status, r.Error.Message)
	}
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("the Claude API answered %s", resp.Status)
	}
	if r.StopReason == "refusal" {
		return "", errors.New("the model declined to analyse this alert")
	}
	var text []string
	for _, c := range r.Content {
		if c.Type == "text" {
			text = append(text, c.Text)
		}
	}
	return strings.Join(text, "\n"), nil
}
