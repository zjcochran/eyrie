// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/zjcochran/eyrie/internal/store"
)

func TestStatusMessages(t *testing.T) {
	n := &Notifier{}
	m := store.Monitor{ID: 5, Name: "Shop website", Type: "http", Description: "the shop"}
	for _, c := range []struct {
		beat         store.Beat
		since        time.Time
		event, title string
		text         string
		priority     int
	}{
		{store.Beat{At: t0, Status: "down", Important: true, Message: "Status 503", Latency: 2 * time.Second}, time.Time{},
			EventDown, "Shop website is down", "Status 503", 4},
		{store.Beat{At: t0, Status: "down", Message: "Status 503", Latency: -1}, t0.Add(-3 * time.Hour),
			EventStillDown, "Shop website is still down", "Down for 3 h. Status 503", 4},
		{store.Beat{At: t0, Status: "down", Message: "Status 503", Latency: -1}, time.Time{},
			EventStillDown, "Shop website is still down", "Status 503", 4},
		{store.Beat{At: t0, Status: "up", Important: true, Message: "200 OK", Latency: time.Millisecond}, t0.Add(-12 * time.Minute),
			EventUp, "Shop website is up", "Up again after 12 min down. 200 OK", 3},
		{store.Beat{At: t0, Status: "up", Message: "200 OK"}, time.Time{}, EventUp, "Shop website is up", "200 OK", 3},
	} {
		msg := n.statusMessage(m, c.beat, c.since)
		if msg.Event != c.event || msg.Title != c.title || msg.Text != c.text || msg.Priority != c.priority ||
			msg.Monitor.Description != "the shop" || msg.URL != "" || !msg.Time.Equal(t0) {
			t.Errorf("%+v: %+v", c.beat, msg)
		}
	}
	if msg := n.statusMessage(m, store.Beat{At: t0, Status: "up", Latency: 1234567 * time.Nanosecond}, time.Time{}); *msg.Heartbeat.LatencyMS != 1.23 {
		t.Errorf("latency %v", *msg.Heartbeat.LatencyMS)
	}
}

func TestCertMessages(t *testing.T) {
	n := &Notifier{BaseURL: "https://eyrie.example.com"}
	m := store.Monitor{ID: 5, Name: "Shop website"}
	exp := time.Date(2027, 2, 1, 12, 0, 0, 0, time.UTC)
	for days, want := range map[int]string{14: "expires in 14 days", 1: "expires in 1 day", -2: "has expired"} {
		msg := n.certMessage(m, exp, days)
		if !strings.Contains(msg.Title, want) || msg.Event != EventCert || !msg.CertExpires.Equal(exp) ||
			msg.URL != "https://eyrie.example.com/monitors/5" || !strings.Contains(msg.Text, "1 February 2027 at 12:00 UTC") {
			t.Errorf("%d days: %+v", days, msg)
		}
		if (days <= 7) != (msg.Priority == 4) {
			t.Errorf("%d days: priority %d", days, msg.Priority)
		}
	}
}

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		45 * time.Second: "45 s", 12 * time.Minute: "12 min", 3*time.Hour + 5*time.Minute: "3 h 5 min",
		2 * time.Hour: "2 h", 52 * time.Hour: "2 d 4 h", 48 * time.Hour: "2 d",
	} {
		if got := Duration(d); got != want {
			t.Errorf("%v: %q", d, got)
		}
	}
}

func TestNtfyTags(t *testing.T) {
	for _, c := range []struct {
		m    Message
		want string
	}{
		{Message{Event: EventUp, Priority: 3}, "white_check_mark"},
		{Message{Event: EventDown, Priority: 4}, "warning"},
		{Message{Event: "test", Priority: 3}, "white_check_mark"},
		{Message{Event: EventCert, Priority: 3}, "information_source"},
	} {
		if got := ntfyTag(c.m); got != c.want {
			t.Errorf("%+v: %s", c.m, got)
		}
	}
	if pushoverPriority(5) != 1 || gotifyPriority(3) != 5 {
		t.Error("priorities")
	}
}
