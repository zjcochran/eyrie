// SPDX-License-Identifier: AGPL-3.0-or-later

// Package notify sends monitors' status changes and certificate warnings to
// ntfy, Gotify, Pushover, Telegram, Discord, email, or any webhook, signed
// with HMAC-SHA256. A channel's URL, token and user are stored sealed, like
// other secrets: a webhook URL can itself be a credential.
package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/zjcochran/eyrie/internal/store"
)

// Channel kinds.
const (
	KindNtfy     = "ntfy"
	KindGotify   = "gotify"
	KindPushover = "pushover"
	KindTelegram = "telegram"
	KindDiscord  = "discord"
	KindEmail    = "email"
	KindWebhook  = "webhook"
)

// Kinds lists the channel kinds in menu order with their names.
var Kinds = []struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}{
	{KindNtfy, "ntfy"}, {KindGotify, "Gotify"}, {KindPushover, "Pushover"}, {KindTelegram, "Telegram"},
	{KindDiscord, "Discord"}, {KindEmail, "Email (SMTP)"}, {KindWebhook, "Webhook (JSON, signed)"},
}

// Service addresses, variables so tests can point them elsewhere.
var (
	PushoverURL = "https://api.pushover.net/1/messages.json"
	TelegramURL = "https://api.telegram.org" // the default; a channel's URL replaces it
)

// Channel is a channel with its URL, token and user in clear.
type Channel struct {
	ID      int64
	Name    string
	Kind    string
	URL     string
	Token   string // ntfy access token, Gotify app token, Pushover app token, Telegram bot token, SMTP password, webhook secret
	User    string // Pushover user key, SMTP username
	To      string // Telegram chat ID; email recipients, comma-separated
	From    string // email sender
	Enabled bool
	Default bool
}

// Validate checks a channel's fields for its kind. Errors are meant for
// people.
func (c Channel) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("give the channel a name")
	}
	switch c.Kind {
	case KindPushover:
		if c.Token == "" || c.User == "" {
			return errors.New("a Pushover channel needs the application token and your user key")
		}
		return nil
	case KindTelegram:
		if c.Token == "" || c.To == "" {
			return errors.New("a Telegram channel needs the bot token and the chat ID")
		}
		if c.URL == "" {
			return nil
		}
	case KindEmail:
		return c.validateEmail()
	case KindGotify:
		if c.Token == "" {
			return errors.New("a Gotify channel needs an application token")
		}
	case KindNtfy, KindDiscord, KindWebhook:
	default:
		return fmt.Errorf("unknown kind %q", c.Kind)
	}
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("the URL must start with http:// or https://")
	}
	return nil
}

// Sealer seals and opens stored secrets.
type Sealer interface {
	Seal(plain string) (string, error)
	Open(sealed string) (string, error)
}

// FromStore opens a stored channel.
func FromStore(sc store.Channel, s Sealer) (Channel, error) {
	c := Channel{ID: sc.ID, Name: sc.Name, Kind: sc.Kind, To: sc.Recipients, From: sc.Sender, Enabled: sc.Enabled, Default: sc.Default}
	var err error
	if c.URL, err = s.Open(sc.URL); err != nil {
		return c, err
	}
	if c.Token, err = s.Open(sc.Token); err != nil {
		return c, err
	}
	c.User, err = s.Open(sc.Username)
	return c, err
}

// ToStore seals a channel for storing.
func ToStore(c Channel, s Sealer) (store.Channel, error) {
	sc := store.Channel{ID: c.ID, Name: c.Name, Kind: c.Kind, Recipients: c.To, Sender: c.From, Enabled: c.Enabled, Default: c.Default}
	var err error
	if sc.URL, err = s.Seal(c.URL); err != nil {
		return sc, err
	}
	if sc.Token, err = s.Seal(c.Token); err != nil {
		return sc, err
	}
	sc.Username, err = s.Seal(c.User)
	return sc, err
}

// SettingKey is the settings row that holds Settings as JSON.
const SettingKey = "notifications"

// Settings are the notification settings that are not per channel.
type Settings struct {
	// CertExpiryDays are the days before a certificate expires when a
	// warning goes out, once each.
	CertExpiryDays []int `json:"cert_expiry_days"`
}

// DefaultSettings warn 21, 14 and 7 days before a certificate expires.
func DefaultSettings() Settings { return Settings{CertExpiryDays: []int{21, 14, 7}} }

// Normalize sorts the days, drops repeats and checks them.
func (s *Settings) Normalize() error {
	days := slices.Clone(s.CertExpiryDays)
	sort.Sort(sort.Reverse(sort.IntSlice(days)))
	s.CertExpiryDays = slices.Compact(days)
	if len(s.CertExpiryDays) > 10 {
		return errors.New("at most 10 certificate warnings")
	}
	for _, d := range s.CertExpiryDays {
		if d < 1 || d > 365 {
			return errors.New("certificate warnings are from 1 to 365 days before expiry")
		}
	}
	return nil
}

// Result is the outcome of the last delivery to a channel.
type Result struct {
	At  time.Time
	Err string // empty when it worked
}

// Notifier delivers messages in the background. Set its fields, then call
// Run.
type Notifier struct {
	Store   *store.Store
	Secrets Sealer
	Log     *slog.Logger
	Client  *http.Client
	Retry   []time.Duration // waits before each retry; nil means 5 s, then 30 s
	Version string          // sent in the User-Agent
	BaseURL string          // EYRIE_BASE_URL: messages link to the monitor when set
	TLS     *tls.Config     // RootCAs for SMTP servers; nil uses the system's
	// Analyst explains down alerts; nil sends them without an analysis.
	Analyst Analyst

	queue        chan job
	once         sync.Once
	mu           sync.Mutex
	last         map[int64]Result
	pending      map[int64]chan struct{} // closed when a monitor's waiting message is queued
	settings     atomic.Pointer[Settings]
	sent, failed atomic.Uint64
}

// Analyst explains why a monitor went down, for its down alert. It
// returns "" without an explanation, and returns within its own timeout.
type Analyst interface {
	Analyze(m store.Monitor, b store.Beat) string
}

// job is a message for a monitor's channels.
type job struct {
	monitorID int64
	msg       Message
}

func (n *Notifier) init() {
	n.once.Do(func() {
		n.queue = make(chan job, 256)
		n.last = map[int64]Result{}
		n.pending = map[int64]chan struct{}{}
		if n.Client == nil {
			n.Client = &http.Client{Timeout: 10 * time.Second}
		}
		if n.Retry == nil {
			n.Retry = []time.Duration{5 * time.Second, 30 * time.Second}
		}
		d := DefaultSettings()
		n.settings.Store(&d)
	})
}

// LoadSettings reads the settings, with their defaults.
func LoadSettings(ctx context.Context, st *store.Store) (Settings, error) {
	s := DefaultSettings()
	_, err := st.GetSetting(ctx, SettingKey, &s)
	return s, err
}

// SetSettings makes saved settings take effect.
func (n *Notifier) SetSettings(s Settings) {
	n.init()
	n.settings.Store(&s)
}

// CertDays are the days before expiry when certificate warnings go out,
// largest first.
func (n *Notifier) CertDays() []int {
	n.init()
	return n.settings.Load().CertExpiryDays
}

// enqueue queues a message without blocking; a full queue drops it.
func (n *Notifier) enqueue(j job) {
	n.init()
	select {
	case n.queue <- j:
	default:
		n.Log.Warn("notification queue full; dropped", "event", j.msg.Event, "monitor_id", j.monitorID)
	}
}

// Run loads the settings and delivers queued messages until ctx ends.
func (n *Notifier) Run(ctx context.Context) {
	n.init()
	if s, err := LoadSettings(ctx, n.Store); err != nil {
		n.Log.Error("cannot read notification settings", "err", err)
	} else {
		n.SetSettings(s)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-n.queue:
			n.deliver(ctx, j)
		}
	}
}

// Counts returns deliveries that worked and that failed after every retry.
func (n *Notifier) Counts() (sent, failed uint64) { return n.sent.Load(), n.failed.Load() }

// Last returns the last delivery result for a channel.
func (n *Notifier) Last(id int64) (Result, bool) {
	n.init()
	n.mu.Lock()
	defer n.mu.Unlock()
	r, ok := n.last[id]
	return r, ok
}

func (n *Notifier) deliver(ctx context.Context, j job) {
	chans, err := n.Store.ChannelsForMonitor(ctx, j.monitorID)
	if err != nil {
		n.Log.Error("cannot read the monitor's channels", "err", err)
		return
	}
	var wg sync.WaitGroup
	for _, sc := range chans {
		wg.Go(func() {
			c, err := FromStore(sc, n.Secrets)
			if err == nil {
				err = n.sendRetrying(ctx, c, j.msg)
			}
			n.record(sc.ID, sc.Name, err)
		})
	}
	wg.Wait()
}

func (n *Notifier) record(id int64, name string, err error) {
	r := Result{At: time.Now()}
	if err != nil {
		r.Err = err.Error()
		n.failed.Add(1)
		n.Log.Warn("notification failed", "channel", name, "err", err)
	} else {
		n.sent.Add(1)
	}
	n.mu.Lock()
	n.last[id] = r
	n.mu.Unlock()
}

func (n *Notifier) sendRetrying(ctx context.Context, c Channel, m Message) error {
	err := n.send(ctx, c, m)
	for _, wait := range n.Retry {
		if err == nil || errors.Is(err, errPermanent) {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		err = n.send(ctx, c, m)
	}
	return err
}

// Test sends a test message to a channel at once, without retries. For a
// saved channel the result is recorded as its last delivery.
func (n *Notifier) Test(ctx context.Context, c Channel) error {
	n.init()
	err := n.send(ctx, c, Message{
		Event: "test", Title: "Test notification", Priority: 3, Time: time.Now().UTC(),
		Text: "Notifications from Eyrie reach this channel.",
	})
	if c.ID != 0 {
		n.record(c.ID, c.Name, err)
	}
	return err
}

// errPermanent marks a refusal that retrying cannot fix (4xx).
var errPermanent = errors.New("refused")

func (n *Notifier) send(ctx context.Context, c Channel, m Message) error {
	if c.Kind == KindEmail {
		return n.sendMail(ctx, c, m)
	}
	req, err := request(ctx, c, m)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Eyrie/"+n.Version)
	resp, err := n.Client.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			// The path can hold a secret: a Discord webhook's, a Telegram bot token.
			if u, perr := url.Parse(ue.URL); perr == nil {
				ue.URL = u.Scheme + "://" + u.Host
			}
		}
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	switch {
	case resp.StatusCode < 300:
		return nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s %s", errPermanent, resp.Status, strings.TrimSpace(string(body)))
	}
	return fmt.Errorf("%s %s", resp.Status, strings.TrimSpace(string(body)))
}

// Signature is the X-Hub-Signature-256 header for a body and secret:
// "sha256=" and the hex HMAC-SHA256, as GitHub signs its webhooks.
func Signature(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// request builds the HTTP request each kind of channel expects.
func request(ctx context.Context, c Channel, m Message) (*http.Request, error) {
	post := func(u string, body []byte, contentType string) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", contentType)
		}
		return req, err
	}
	text := withAnalysis(m)
	if m.URL != "" {
		text += "\n" + m.URL
	}
	switch c.Kind {
	case KindNtfy:
		// The URL is the topic, e.g. https://ntfy.sh/my-topic.
		req, err := post(c.URL, []byte(text), "text/plain; charset=utf-8")
		if err != nil {
			return nil, err
		}
		req.Header.Set("Title", m.Title)
		req.Header.Set("Priority", fmt.Sprint(m.Priority))
		req.Header.Set("Tags", ntfyTag(m))
		if m.URL != "" {
			req.Header.Set("Click", m.URL)
		}
		if c.Token != "" {
			req.Header.Set("Authorization", "Bearer "+c.Token)
		}
		return req, nil
	case KindGotify:
		// The URL is the server; the token is an application token.
		b, _ := json.Marshal(map[string]any{"title": m.Title, "message": text, "priority": gotifyPriority(m.Priority)})
		req, err := post(strings.TrimRight(c.URL, "/")+"/message", b, "application/json")
		if err != nil {
			return nil, err
		}
		req.Header.Set("X-Gotify-Key", c.Token)
		return req, nil
	case KindPushover:
		// Pushover takes at most 1024 characters.
		form := url.Values{"token": {c.Token}, "user": {c.User}, "title": {m.Title}, "message": {shorten(withAnalysis(m), 1024)},
			"priority": {fmt.Sprint(pushoverPriority(m.Priority))}, "timestamp": {fmt.Sprint(m.Time.Unix())}}
		if m.URL != "" {
			form.Set("url", m.URL)
		}
		return post(PushoverURL, []byte(form.Encode()), "application/x-www-form-urlencoded")
	case KindTelegram:
		base := TelegramURL
		if c.URL != "" {
			base = c.URL
		}
		b, _ := json.Marshal(map[string]any{
			"chat_id": c.To, "text": shorten(m.Title+"\n\n"+text, 4096), "disable_web_page_preview": true,
		})
		return post(strings.TrimRight(base, "/")+"/bot"+c.Token+"/sendMessage", b, "application/json")
	case KindDiscord:
		// The URL is a channel webhook; it carries its own secret.
		b, _ := json.Marshal(map[string]any{"username": "Eyrie", "content": shorten("**"+m.Title+"**\n"+text, 2000)})
		return post(c.URL, b, "application/json")
	default:
		b, _ := json.Marshal(m)
		req, err := post(c.URL, b, "application/json")
		if err != nil {
			return nil, err
		}
		if c.Token != "" {
			req.Header.Set("X-Hub-Signature-256", Signature(b, c.Token))
		}
		return req, nil
	}
}

// withAnalysis is the message's text followed by Claude's analysis.
func withAnalysis(m Message) string {
	if m.Analysis == "" {
		return m.Text
	}
	return m.Text + "\n\nClaude's analysis: " + m.Analysis
}

// shorten cuts s to at most n characters (Telegram and Discord limits),
// ending it with "…" when cut.
func shorten(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func ntfyTag(m Message) string {
	switch {
	case m.Event == EventUp:
		return "white_check_mark"
	case m.Priority >= 4:
		return "warning"
	case m.Event == "test":
		return "white_check_mark"
	}
	return "information_source"
}

// pushoverPriority maps 1-5 to Pushover's -2 to 1. Pushover's 2 (emergency)
// repeats until acknowledged, so it is not used.
func pushoverPriority(p int) int { return map[int]int{1: -2, 2: -1, 3: 0, 4: 1, 5: 1}[p] }

// gotifyPriority maps 1-5 to Gotify's 0-10 scale.
func gotifyPriority(p int) int { return map[int]int{1: 1, 2: 3, 3: 5, 4: 8, 5: 10}[p] }
