// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zjcochran/eyrie/internal/store"
)

// prefix "seals" by prefixing, so tests can see what was sealed.
type prefix struct{}

func (prefix) Seal(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	return "sealed:" + s, nil
}

func (prefix) Open(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if !strings.HasPrefix(s, "sealed:") {
		return "", errors.New("not sealed")
	}
	return strings.TrimPrefix(s, "sealed:"), nil
}

type hit struct {
	path, body string
	header     http.Header
}

// receiver records requests and answers with the next status in codes
// (200 once they run out).
type receiver struct {
	srv   *httptest.Server
	mu    sync.Mutex
	hits  []hit
	codes []int
}

func newReceiver(t *testing.T, codes ...int) *receiver {
	r := &receiver{codes: codes}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.hits = append(r.hits, hit{req.URL.Path, string(b), req.Header.Clone()})
		code := http.StatusOK
		if len(r.codes) > 0 {
			code, r.codes = r.codes[0], r.codes[1:]
		}
		r.mu.Unlock()
		w.WriteHeader(code)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *receiver) got() []hit {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]hit(nil), r.hits...)
}

func newNotifier(t *testing.T) *Notifier {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	n := &Notifier{Store: st, Secrets: prefix{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Retry: []time.Duration{time.Millisecond, time.Millisecond}, Version: "test", BaseURL: "https://eyrie.example.com"}
	n.init()
	return n
}

var t0 = time.Unix(1_800_000_000, 0).UTC()

func testMonitor() store.Monitor { return store.Monitor{ID: 3, Name: "DB", Type: "port"} }

func testMessage() Message {
	n := &Notifier{BaseURL: "https://eyrie.example.com"}
	return n.statusMessage(testMonitor(), store.Beat{MonitorID: 3, At: t0, Status: "down", Latency: -1,
		Message: "connection refused", Important: true}, time.Time{})
}

// sendAll sends the test message to each channel, whose URLs are paths
// on a receiver, and returns the requests.
func sendAll(t *testing.T, chans ...Channel) []hit {
	t.Helper()
	n := newNotifier(t)
	r := newReceiver(t)
	for _, c := range chans {
		c.URL = r.srv.URL + c.URL
		if err := n.send(context.Background(), c, testMessage()); err != nil {
			t.Fatalf("%s: %v", c.Kind, err)
		}
	}
	return r.got()
}

func TestNtfy(t *testing.T) {
	h := sendAll(t, Channel{Kind: KindNtfy, URL: "/alerts", Token: "tk"})[0]
	for k, want := range map[string]string{
		"Title": "DB is down", "Priority": "4", "Tags": "warning", "Authorization": "Bearer tk",
		"Click": "https://eyrie.example.com/monitors/3", "User-Agent": "Eyrie/test",
	} {
		if got := h.header.Get(k); got != want {
			t.Errorf("%s: %q", k, got)
		}
	}
	if h.path != "/alerts" || h.body != "connection refused\nhttps://eyrie.example.com/monitors/3" {
		t.Errorf("ntfy %+v", h)
	}
}

func TestGotifyTelegramDiscord(t *testing.T) {
	hits := sendAll(t,
		Channel{Kind: KindGotify, URL: "/", Token: "app"},
		Channel{Kind: KindTelegram, Token: "123:abc", To: "-100200"},
		Channel{Kind: KindDiscord, URL: "/api/webhooks/1/x"},
	)
	var gotify, tg map[string]any
	json.Unmarshal([]byte(hits[0].body), &gotify)
	if hits[0].path != "/message" || hits[0].header.Get("X-Gotify-Key") != "app" || gotify["priority"] != float64(8) {
		t.Errorf("gotify %+v %v", hits[0], gotify)
	}
	json.Unmarshal([]byte(hits[1].body), &tg)
	if hits[1].path != "/bot123:abc/sendMessage" || tg["chat_id"] != "-100200" ||
		tg["text"] != "DB is down\n\nconnection refused\nhttps://eyrie.example.com/monitors/3" {
		t.Errorf("telegram %+v %v", hits[1], tg)
	}
	if !strings.Contains(hits[2].body, `"username":"Eyrie"`) || !strings.Contains(hits[2].body, `**DB is down**`) {
		t.Errorf("discord %s", hits[2].body)
	}
}

func TestWebhook(t *testing.T) {
	hits := sendAll(t, Channel{Kind: KindWebhook, URL: "/hook", Token: "s3cret"}, Channel{Kind: KindWebhook, URL: "/unsigned"})
	sig := hits[0].header.Get("X-Hub-Signature-256")
	if sig != Signature([]byte(hits[0].body), "s3cret") || !strings.HasPrefix(sig, "sha256=") {
		t.Errorf("signature %q", sig)
	}
	var p Message
	if err := json.Unmarshal([]byte(hits[0].body), &p); err != nil || p.Event != EventDown || p.Monitor.Name != "DB" ||
		p.Heartbeat.Status != "down" || p.Heartbeat.LatencyMS != nil || p.URL == "" {
		t.Errorf("webhook payload %s %v", hits[0].body, err)
	}
	if hits[1].header.Get("X-Hub-Signature-256") != "" {
		t.Error("signed without a secret")
	}
}

func TestPushover(t *testing.T) {
	n := newNotifier(t)
	r := newReceiver(t)
	old := PushoverURL
	PushoverURL = r.srv.URL + "/1/messages.json"
	defer func() { PushoverURL = old }()
	c := Channel{Name: "p", Kind: KindPushover, Token: "app-token", User: "user-key"}
	if err := n.send(context.Background(), c, testMessage()); err != nil {
		t.Fatal(err)
	}
	h := r.got()[0]
	form, _ := url.ParseQuery(h.body)
	if h.path != "/1/messages.json" || form.Get("token") != "app-token" || form.Get("user") != "user-key" ||
		form.Get("title") != "DB is down" || form.Get("priority") != "1" || form.Get("timestamp") != "1800000000" ||
		form.Get("url") != "https://eyrie.example.com/monitors/3" {
		t.Errorf("pushover request = %s %v", h.path, form)
	}
}

func TestSignature(t *testing.T) {
	// The example from GitHub's documentation on validating webhooks.
	if got := Signature([]byte("Hello, World!"), "It's a Secret to Everybody"); got != "sha256=757107ea0eb2509fc211221cce984b8a37570b6d7586c22c46f4379c8b043e17" {
		t.Errorf("signature %s", got)
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection refused")
}

func TestErrorsHideTheURLPath(t *testing.T) {
	n := newNotifier(t)
	n.Client = &http.Client{Transport: failingTransport{}}
	for _, c := range []Channel{
		{Name: "t", Kind: KindTelegram, Token: "123:secret-bot-token", To: "1"},
		{Name: "d", Kind: KindDiscord, URL: "https://discord.example/api/webhooks/1/secret-webhook-token"},
	} {
		err := n.send(context.Background(), c, testMessage())
		if err == nil || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "connection refused") {
			t.Errorf("%s: error %v", c.Kind, err)
		}
	}
}

func TestValidate(t *testing.T) {
	good := []Channel{
		{Name: "x", Kind: KindNtfy, URL: "https://ntfy.example.com/alerts"},
		{Name: "x", Kind: KindGotify, URL: "https://gotify.example.com", Token: "a"},
		{Name: "x", Kind: KindPushover, Token: "a", User: "u"},
		{Name: "x", Kind: KindTelegram, Token: "a", To: "1"},
		{Name: "x", Kind: KindTelegram, Token: "a", To: "1", URL: "https://bot.example"},
		{Name: "x", Kind: KindDiscord, URL: "https://discord.example/api/webhooks/1/x"},
		{Name: "x", Kind: KindWebhook, URL: "http://hooks.example.com/eyrie"},
		{Name: "x", Kind: KindEmail, URL: "smtp://mail.example.com", From: "eyrie@example.com", To: "alice@example.com, bob@example.com"},
	}
	for _, c := range good {
		if err := c.Validate(); err != nil {
			t.Errorf("%+v: %v", c, err)
		}
	}
	bad := []Channel{
		{Kind: KindNtfy, URL: "https://ntfy.example.com/alerts"},
		{Name: "x", Kind: "carrier-pigeon"},
		{Name: "x", Kind: KindNtfy, URL: "ntfy.example.com"},
		{Name: "x", Kind: KindPushover, Token: "a"},
		{Name: "x", Kind: KindTelegram, Token: "a"},
		{Name: "x", Kind: KindTelegram, Token: "a", To: "1", URL: "ftp://bot.example"},
		{Name: "x", Kind: KindGotify, URL: "https://gotify.example"},
		{Name: "x", Kind: KindEmail, URL: "https://mail.example.com", From: "a@example.com", To: "b@example.com"},
		{Name: "x", Kind: KindEmail, URL: "smtp://mail.example.com", From: "nobody", To: "b@example.com"},
		{Name: "x", Kind: KindEmail, URL: "smtp://mail.example.com", From: "a@example.com", To: "bob"},
	}
	for _, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("%+v was accepted", c)
		}
	}
}

// link saves a channel for monitor 1 and returns it.
func link(t *testing.T, n *Notifier, c Channel) store.Channel {
	t.Helper()
	ctx := context.Background()
	if _, err := n.Store.MonitorByID(ctx, 1); err != nil {
		n.Store.CreateMonitor(ctx, store.Monitor{Name: "DB", Type: "port", Active: true, Interval: time.Minute,
			RetryInterval: time.Minute, Timeout: time.Second, Config: json.RawMessage(`{}`)}, t0)
	}
	sc, err := ToStore(c, prefix{})
	if err != nil {
		t.Fatal(err)
	}
	sc, err = n.Store.CreateChannel(ctx, sc, true, t0)
	if err != nil {
		t.Fatal(err)
	}
	return sc
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for range 300 {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestDelivery(t *testing.T) {
	n := newNotifier(t)
	ok := newReceiver(t)
	flaky := newReceiver(t, http.StatusBadGateway) // then 200
	refusing := newReceiver(t, http.StatusUnauthorized)
	a := link(t, n, Channel{Name: "A", Kind: KindWebhook, URL: ok.srv.URL, Enabled: true})
	b := link(t, n, Channel{Name: "B", Kind: KindWebhook, URL: flaky.srv.URL, Enabled: true})
	c := link(t, n, Channel{Name: "C", Kind: KindWebhook, URL: refusing.srv.URL, Enabled: true})
	link(t, n, Channel{Name: "Off", Kind: KindWebhook, URL: ok.srv.URL, Enabled: false})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.Run(ctx)
	n.Monitor(store.Monitor{ID: 1, Name: "DB"}, store.Beat{MonitorID: 1, At: t0, Status: "down", Important: true, Latency: -1}, time.Time{})
	waitFor(t, "every delivery", func() bool {
		_, okA := n.Last(a.ID)
		_, okB := n.Last(b.ID)
		_, okC := n.Last(c.ID)
		return okA && okB && okC
	})
	if len(ok.got()) != 1 || len(flaky.got()) != 2 || len(refusing.got()) != 1 {
		t.Errorf("requests: %d %d %d", len(ok.got()), len(flaky.got()), len(refusing.got()))
	}
	if r, _ := n.Last(c.ID); !strings.Contains(r.Err, "401") {
		t.Errorf("refused: %+v", r)
	}
	if sent, failed := n.Counts(); sent != 2 || failed != 1 {
		t.Errorf("counts %d %d", sent, failed)
	}
}

func TestDeliveryProblems(t *testing.T) {
	n := newNotifier(t)
	// A channel whose secret does not open.
	sc, _ := n.Store.CreateChannel(context.Background(), store.Channel{Name: "Broken", Kind: KindWebhook, URL: "garbage", Enabled: true}, false, t0)
	n.Store.CreateMonitor(context.Background(), store.Monitor{Name: "DB", Type: "port", Active: true, Interval: time.Minute,
		RetryInterval: time.Minute, Timeout: time.Second, Config: json.RawMessage(`{}`)}, t0)
	n.Store.SetMonitorChannels(context.Background(), 1, []int64{sc.ID})
	n.deliver(context.Background(), job{1, testMessage()})
	if r, ok := n.Last(sc.ID); !ok || r.Err == "" {
		t.Errorf("broken channel: %+v %v", r, ok)
	}
	// A full queue drops messages instead of blocking.
	for range cap(n.queue) + 5 {
		n.Monitor(testMonitor(), store.Beat{Status: "up"}, time.Time{})
	}
	if len(n.queue) != cap(n.queue) {
		t.Errorf("queue %d", len(n.queue))
	}
	// Retrying stops when the context ends.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n.Retry = []time.Duration{time.Hour}
	r := newReceiver(t, http.StatusBadGateway)
	if err := n.sendRetrying(ctx, Channel{Kind: KindWebhook, URL: r.srv.URL}, testMessage()); err == nil {
		t.Error("retried after the context ended")
	}
	n.Store.Close()
	n.deliver(context.Background(), job{1, testMessage()}) // logged, not a panic
}

func TestTest(t *testing.T) {
	n := newNotifier(t)
	r := newReceiver(t)
	if err := n.Test(context.Background(), Channel{ID: 7, Name: "Saved", Kind: KindWebhook, URL: r.srv.URL}); err != nil {
		t.Fatal(err)
	}
	if res, ok := n.Last(7); !ok || res.Err != "" {
		t.Errorf("last %+v %v", res, ok)
	}
	if err := n.Test(context.Background(), Channel{Kind: KindWebhook, URL: r.srv.URL}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.got()[0].body, `"event":"test"`) {
		t.Errorf("test message %s", r.got()[0].body)
	}
}

func TestTestAlert(t *testing.T) {
	n := newNotifier(t)
	r := newReceiver(t)
	if err := n.TestAlert(context.Background(), Channel{ID: 3, Name: "Hook", Kind: KindWebhook, URL: r.srv.URL}, "Upstream down."); err != nil {
		t.Fatal(err)
	}
	var m Message
	json.Unmarshal([]byte(r.got()[0].body), &m)
	if m.Event != "test" || m.Analysis != "Upstream down." || !strings.Contains(m.Title, "Shop website") {
		t.Errorf("message %+v", m)
	}
	if res, ok := n.Last(3); !ok || res.Err != "" {
		t.Errorf("last %+v %v", res, ok)
	}
}

func TestStoreRoundTrip(t *testing.T) {
	c := Channel{ID: 2, Name: "Mail", Kind: KindEmail, URL: "smtp://mail.example.com", Token: "pw", User: "alice",
		To: "bob@example.com", From: "eyrie@example.com", Enabled: true, Default: true}
	sc, err := ToStore(c, prefix{})
	if err != nil || sc.URL != "sealed:smtp://mail.example.com" || sc.Username != "sealed:alice" || sc.Recipients != c.To {
		t.Fatalf("to store %+v %v", sc, err)
	}
	back, err := FromStore(sc, prefix{})
	if err != nil || back != c {
		t.Errorf("round trip %+v %v", back, err)
	}
	for _, broken := range []store.Channel{{URL: "x"}, {Token: "x"}, {Username: "x"}} {
		if _, err := FromStore(broken, prefix{}); err == nil {
			t.Errorf("opened %+v", broken)
		}
	}
	if _, err := ToStore(c, failSealer{}); err == nil {
		t.Error("sealed with a broken sealer")
	}
}

type failSealer struct{ prefix }

func (failSealer) Seal(string) (string, error) { return "", errors.New("no key") }

func TestSettings(t *testing.T) {
	s := Settings{CertExpiryDays: []int{7, 21, 14, 7}}
	if err := s.Normalize(); err != nil || len(s.CertExpiryDays) != 3 || s.CertExpiryDays[0] != 21 {
		t.Errorf("normalize %+v %v", s, err)
	}
	for _, bad := range [][]int{{0}, {400}, {1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11}} {
		s := Settings{CertExpiryDays: bad}
		if s.Normalize() == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	n := newNotifier(t)
	if d := n.CertDays(); len(d) != 3 || d[0] != 21 {
		t.Errorf("defaults %v", d)
	}
	n.Store.SetSetting(context.Background(), SettingKey, Settings{CertExpiryDays: []int{30}}, t0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { n.Run(ctx); close(done) }()
	waitFor(t, "settings loaded", func() bool { return len(n.CertDays()) == 1 })
	cancel()
	<-done
	n.SetSettings(Settings{CertExpiryDays: []int{5}})
	if n.CertDays()[0] != 5 {
		t.Error("set")
	}
}

func TestRunWithoutDatabase(t *testing.T) {
	n := newNotifier(t)
	n.Store.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n.Run(ctx) // logs, returns
}
