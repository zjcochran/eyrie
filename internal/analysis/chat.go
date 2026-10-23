// SPDX-License-Identifier: AGPL-3.0-or-later

package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

type chatRequest struct {
	Model    string    `json:"model"`
	Messages []message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	// Error is {"message": "..."} on most servers, a string on some.
	Error json.RawMessage `json:"error"`
}

// thinking is the reasoning some local models write before their answer.
var thinking = regexp.MustCompile(`(?s)<think>.*?</think>`)

// chat asks a model on an OpenAI-compatible server (POST
// <base URL>/chat/completions), with an API key when one is set.
func (a *Analyzer) chat(ctx context.Context, s Settings, key, prompt string) (string, error) {
	h := http.Header{}
	if key != "" {
		h.Set("Authorization", "Bearer "+key)
	}
	body := chatRequest{Model: s.Model, Messages: []message{{"system", system}, {"user", prompt}}}
	resp, err := a.post(ctx, s.BaseURL+"/chat/completions", h, body)
	if err != nil {
		return "", err
	}
	var r chatResponse
	if err := json.Unmarshal(resp.Body, &r); err != nil {
		return "", fmt.Errorf("the model server answered %s", resp.Status)
	}
	if msg := errorMessage(r.Error); msg != "" {
		return "", fmt.Errorf("the model server answered %s: %s", resp.Status, msg)
	}
	if resp.Code >= 300 || len(r.Choices) == 0 {
		return "", fmt.Errorf("the model server answered %s", resp.Status)
	}
	if r.Choices[0].FinishReason == "content_filter" {
		return "", errors.New("the model declined to analyse this alert")
	}
	return thinking.ReplaceAllString(r.Choices[0].Message.Content, ""), nil
}

func errorMessage(raw json.RawMessage) string {
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Message != "" {
		return e.Message
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	return ""
}
