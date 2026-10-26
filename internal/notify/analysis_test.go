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

func TestDownAlertDoesNotWaitForAnalysis(t *testing.T) {
	n := newNotifier(t)
	r := newReceiver(t)
	link(t, n, Channel{Name: "A", Kind: KindWebhook, URL: r.srv.URL, Enabled: true})
	a := slowAnalyst{make(chan struct{}), "Likely cause: the database is out of connections."}
	n.Analyst = a
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.Run(ctx)
	m := store.Monitor{ID: 1, Name: "DB"}
	n.Monitor(m, store.Beat{MonitorID: 1, At: t0, Status: "down", Important: true, Latency: -1, Message: "connection refused"}, time.Time{})
	n.Monitor(m, store.Beat{MonitorID: 1, At: t0.Add(time.Minute), Status: "up", Important: true}, t0)
	waitFor(t, "the down and up alerts", func() bool { return len(r.got()) == 2 })
	close(a.release)
	waitFor(t, "the analysis", func() bool { return len(r.got()) == 3 })
	var down, up, analysis Message
	json.Unmarshal([]byte(r.got()[0].body), &down)
	json.Unmarshal([]byte(r.got()[1].body), &up)
	json.Unmarshal([]byte(r.got()[2].body), &analysis)
	if down.Event != EventDown || down.Text != "connection refused" || up.Event != EventUp {
		t.Errorf("down %+v\nup %+v", down, up)
	}
	if analysis.Event != EventAnalysis || analysis.Title != "DB is down: analysis" || analysis.Text != a.answer ||
		analysis.Priority != 3 || analysis.Monitor == nil || analysis.Monitor.ID != 1 || analysis.Heartbeat == nil {
		t.Errorf("analysis %+v", analysis)
	}
}

func TestNoAnswerNoAnalysisMessage(t *testing.T) {
	n := newNotifier(t)
	r := newReceiver(t)
	link(t, n, Channel{Name: "A", Kind: KindWebhook, URL: r.srv.URL, Enabled: true})
	a := slowAnalyst{make(chan struct{}), ""}
	close(a.release)
	n.Analyst = a
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.Run(ctx)
	n.Monitor(store.Monitor{ID: 1, Name: "DB"}, store.Beat{MonitorID: 1, At: t0, Status: "down", Important: true}, time.Time{})
	waitFor(t, "the down alert", func() bool { return len(r.got()) == 1 })
	time.Sleep(50 * time.Millisecond)
	if got := r.got(); len(got) != 1 {
		t.Errorf("sent %v", got)
	}
}

func TestMailLineBreaks(t *testing.T) {
	m := testMessage()
	m.Text = "Likely cause: the database stopped.\nCheck first: its service."
	mail := string(mailMessage(mustAddr("eyrie@example.com"), nil, m))
	if !strings.Contains(mail, "Likely cause: the database stopped.\r\nCheck first: its service.") {
		t.Errorf("mail %s", mail)
	}
}

func TestLongMessagesFit(t *testing.T) {
	n := newNotifier(t)
	r := newReceiver(t)
	old := PushoverURL
	PushoverURL = r.srv.URL + "/p"
	defer func() { PushoverURL = old }()
	m := testMessage()
	m.Text = strings.Repeat("é", 5000)
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
