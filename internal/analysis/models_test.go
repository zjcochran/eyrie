// SPDX-License-Identifier: AGPL-3.0-or-later

package analysis

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestModels(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		if strings.HasPrefix(r.URL.Path, "/v1/models") && r.Header.Get("X-Api-Key") != "" {
			io.WriteString(w, `{"data":[{"id":"claude-opus-5-5","display_name":"Claude Opus 5.5"},{"id":"claude-sonnet-5-5"}],"has_more":false}`)
			return
		}
		io.WriteString(w, `{"object":"list","data":[{"id":"qwen3:14b"},{"id":""},{"id":"gemma3:12b"}]}`)
	}))
	defer srv.Close()
	a := newAnalyzer(t)
	a.APIURL = srv.URL
	models, err := a.Models(context.Background(), Settings{Provider: ProviderAPI, APIKey: "sk-ant-api03-k"})
	if err != nil || !slices.Equal(models, []string{"claude-opus-5-5", "claude-sonnet-5-5"}) {
		t.Errorf("api %v %v", models, err)
	}
	if got.Method != http.MethodGet || got.URL.RawQuery != "limit=1000" || got.Header.Get("Anthropic-Version") != "2023-06-01" {
		t.Errorf("api request %s %s %v", got.Method, got.URL, got.Header)
	}
	models, err = a.Models(context.Background(), Settings{Provider: ProviderLocal, BaseURL: srv.URL + "/v1", LocalKey: "l0cal"})
	if err != nil || !slices.Equal(models, []string{"gemma3:12b", "qwen3:14b"}) {
		t.Errorf("local %v %v", models, err)
	}
	if got.URL.Path != "/v1/models" || got.Header.Get("Authorization") != "Bearer l0cal" {
		t.Errorf("local request %s %v", got.URL, got.Header)
	}
	if _, err = a.Models(context.Background(), Settings{Provider: ProviderLocal, BaseURL: srv.URL}); err != nil ||
		got.Header.Get("Authorization") != "" {
		t.Errorf("no key %v %v", err, got.Header)
	}
}

func TestModelsErrors(t *testing.T) {
	a := newAnalyzer(t)
	for want, s := range map[string]Settings{
		"server URL first": {Provider: ProviderLocal},
		"API key first":    {Provider: ProviderAPI},
	} {
		if _, err := a.Models(context.Background(), s); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: %v", s, err)
		}
	}
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{http.StatusUnauthorized, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`,
			"the Claude API answered 401 Unauthorized: invalid x-api-key"},
		{http.StatusBadGateway, `<html>`, "answered 502 Bad Gateway"},
		{http.StatusTeapot, `{}`, "answered 418"},
	}
	for _, c := range cases {
		srv, _, _ := fakeServer(t, c.status, c.body)
		a.APIURL = srv.URL
		if _, err := a.Models(context.Background(), Settings{Provider: ProviderAPI, APIKey: "k"}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%d: %v", c.status, err)
		}
	}
	srv, _, _ := fakeServer(t, http.StatusNotFound, `{"error":"not found"}`)
	if _, err := a.Models(context.Background(), Settings{Provider: ProviderLocal, BaseURL: srv.URL}); err == nil ||
		err.Error() != "the model server answered 404 Not Found: not found" {
		t.Errorf("local: %v", err)
	}
	a.Client = &http.Client{Transport: failing{}}
	if _, err := a.Models(context.Background(), Settings{Provider: ProviderLocal, BaseURL: "http://192.0.2.20:11434/v1"}); err == nil {
		t.Error("offline")
	}
}
