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

func (a *Analyzer) apiURL() string {
	if a.APIURL == "" {
		return "https://api.anthropic.com"
	}
	return a.APIURL
}

func anthropicHeaders(key string) http.Header {
	return http.Header{"X-Api-Key": {key}, "Anthropic-Version": {"2023-06-01"}}
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
	h := anthropicHeaders(key)
	if body.Fallbacks != "" {
		h.Set("Anthropic-Beta", "server-side-fallback-2026-07-01")
	}
	resp, err := a.post(ctx, a.apiURL()+"/v1/messages", h, body)
	if err != nil {
		return "", err
	}
	var r messagesResponse
	if err := json.Unmarshal(resp.Body, &r); err != nil {
		return "", fmt.Errorf("the Claude API answered %s", resp.Status)
	}
	if r.Error != nil {
		return "", fmt.Errorf("the Claude API answered %s: %s", resp.Status, r.Error.Message)
	}
	if resp.Code >= 300 {
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

// answer is an HTTP answer: its status and at most 1 MiB of its body.
type answer struct {
	Status string
	Code   int
	Body   []byte
}

// post sends body as JSON with headers h.
func (a *Analyzer) post(ctx context.Context, url string, h http.Header, body any) (answer, error) {
	b, _ := json.Marshal(body)
	h.Set("Content-Type", "application/json")
	return a.do(ctx, http.MethodPost, url, h, bytes.NewReader(b))
}

// do sends a request with headers h.
func (a *Analyzer) do(ctx context.Context, method, url string, h http.Header, body io.Reader) (answer, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return answer{}, err
	}
	req.Header = h
	req.Header.Set("User-Agent", "Eyrie/"+a.Version)
	client := a.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return answer{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return answer{resp.Status, resp.StatusCode, raw}, err
}
