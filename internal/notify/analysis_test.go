// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"context"
	"encoding/json"
	"net/mail"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zjcochran/eyrie/internal/store"
)

// slowAnalyst answers once release is closed.
type slowAnalyst struct {
	release chan struct{}
	answer  string
}

func (s slowAnalyst) Analyze(store.Monitor, store.Beat) string {
	<-s.release
	return s.answer
}

func TestAnalysisWaitsAndKeepsOrder(t *testing.T) {
	n := newNotifier(t)
	r := newReceiver(t)
	link(t, n, Channel{Name: "A", Kind: KindWebhook, URL: r.srv.URL, Enabled: true})
	a := slowAnalyst{make(chan struct{}), "The database is out of connections."}
	n.Analyst = a
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.Run(ctx)
	m := store.Monitor{ID: 1, Name: "DB"}
	n.Monitor(m, store.Beat{MonitorID: 1, At: t0, Status: "down", Important: true, Latency: -1}, time.Time{})
	n.Monitor(m, store.Beat{MonitorID: 1, At: t0.Add(time.Minute), Status: "up", Important: true}, t0)
	// Another monitor's message does not wait.
	n.Monitor(store.Monitor{ID: 2, Name: "Other"}, store.Beat{MonitorID: 2, At: t0, Status: "up", Important: true}, t0)
	time.Sleep(50 * time.Millisecond)
	if got := r.got(); len(got) != 0 {
		t.Fatalf("sent before the analysis: %v", got)
	}
	close(a.release)
	waitFor(t, "both messages", func() bool { return len(r.got()) == 2 })
	var down, up Message
	json.Unmarshal([]byte(r.got()[0].body), &down)
	json.Unmarshal([]byte(r.got()[1].body), &up)
	if down.Event != EventDown || down.Analysis != a.answer || up.Event != EventUp || up.Analysis != "" {
		t.Errorf("down %+v\nup %+v", down, up)
	}
	n.mu.Lock()
	left := len(n.pending)
	n.mu.Unlock()
	if left != 0 {
		t.Errorf("%d pending left", left)
	}
}

func analysed() Message {
	m := testMessage()
	m.Analysis = "Port 5432 refuses connections: the database is likely stopped."
	return m
}

func TestAnalysisInMessages(t *testing.T) {
	n := newNotifier(t)
	r := newReceiver(t)
	for _, c := range []Channel{{Kind: KindNtfy, URL: "/t"}, {Kind: KindTelegram, Token: "1:a", To: "5"}, {Kind: KindDiscord, URL: "/d"}} {
		if c.URL != "" || c.Kind == KindTelegram {
			c.URL = r.srv.URL + c.URL
		}
		if err := n.send(context.Background(), c, analysed()); err != nil {
			t.Fatal(err)
		}
	}
	want := "connection refused\n\nAnalysis: Port 5432 refuses connections: the database is likely stopped.\nhttps://eyrie.example.com/monitors/3"
	hits := r.got()
	if hits[0].body != want {
		t.Errorf("ntfy %q", hits[0].body)
	}
	for _, h := range hits[1:] {
		if !strings.Contains(h.body, "Analysis: Port 5432") {
			t.Errorf("%s %s", h.path, h.body)
		}
	}
	mail := string(mailMessage(mustAddr("eyrie@example.com"), nil, analysed()))
	if !strings.Contains(mail, "Analysis:\r\nPort 5432 refuses connections: the database is likely stopped.") {
		t.Errorf("mail %s", mail)
	}
}

func TestLongMessagesFit(t *testing.T) {
	n := newNotifier(t)
	r := newReceiver(t)
	old := PushoverURL
	PushoverURL = r.srv.URL + "/p"
	defer func() { PushoverURL = old }()
	m := analysed()
	m.Analysis = strings.Repeat("é", 5000)
	for _, c := range []Channel{{Kind: KindPushover, Token: "t", User: "u"}, {Kind: KindDiscord, URL: r.srv.URL + "/d"},
		{Kind: KindTelegram, URL: r.srv.URL, Token: "1:a", To: "5"}} {
		if err := n.send(context.Background(), c, m); err != nil {
			t.Fatal(err)
		}
	}
	hits := r.got()
	form, _ := url.ParseQuery(hits[0].body)
	var discord, tg map[string]string
	json.Unmarshal([]byte(hits[1].body), &discord)
	json.Unmarshal([]byte(hits[2].body), &tg)
	for name, s := range map[string]struct {
		text string
		max  int
	}{"pushover": {form.Get("message"), 1024}, "discord": {discord["content"], 2000}, "telegram": {tg["text"], 4096}} {
		if n := len([]rune(s.text)); n != s.max || !strings.HasSuffix(s.text, "…") {
			t.Errorf("%s: %d characters", name, n)
		}
	}
}

func mustAddr(s string) *mail.Address {
	a, _ := mail.ParseAddress(s)
	return a
}
