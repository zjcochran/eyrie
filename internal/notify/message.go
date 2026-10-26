// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/zjcochran/eyrie/internal/store"
)

// Events a message can be about.
const (
	EventDown      = "monitor_down"
	EventStillDown = "monitor_still_down"
	EventUp        = "monitor_up"
	EventCert      = "certificate_expiring"
	EventAnalysis  = "monitor_analysis"
)

// Message is what a channel receives. The webhook kind gets it as JSON.
type Message struct {
	Event     string       `json:"event"`
	Title     string       `json:"title"`
	Text      string       `json:"message"`
	Priority  int          `json:"priority"` // 1 (lowest) to 5 (urgent), as in ntfy
	Time      time.Time    `json:"time"`
	URL       string       `json:"url,omitempty"` // the monitor's page, when EYRIE_BASE_URL is set
	Monitor   *MonitorInfo `json:"monitor,omitempty"`
	Heartbeat *BeatInfo    `json:"heartbeat,omitempty"`
	// CertExpires is when the certificate expires (certificate warnings).
	CertExpires *time.Time `json:"cert_expires,omitempty"`
}

// MonitorInfo describes the monitor a message is about.
type MonitorInfo struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// BeatInfo is the heartbeat that caused a message.
type BeatInfo struct {
	Status    string    `json:"status"`
	At        time.Time `json:"at"`
	LatencyMS *float64  `json:"latency_ms"`
	Message   string    `json:"message"`
}

// Monitor queues a message about a monitor's status, for the channels it
// uses: down, back up, or still down. since is when the status before this
// one began (how long it was down, for "back up"), or when the down status
// began (for "still down").
//
// With an Analyst, a down alert is followed by a second message with its
// analysis, once the model answers. The down alert never waits for it.
func (n *Notifier) Monitor(m store.Monitor, b store.Beat, since time.Time) {
	n.init()
	msg := n.statusMessage(m, b, since)
	n.enqueue(job{m.ID, msg})
	if msg.Event != EventDown || n.Analyst == nil {
		return
	}
	go func() {
		if text := n.Analyst.Analyze(m, b); text != "" {
			n.enqueue(job{m.ID, analysisMessage(msg, text)})
		}
	}()
}

// analysisMessage is the message that follows a down alert with the
// model's explanation of it.
func analysisMessage(down Message, text string) Message {
	down.Event, down.Title, down.Priority, down.Text = EventAnalysis, down.Title+": analysis", 3, text
	down.Time = time.Now().UTC()
	return down
}

func (n *Notifier) statusMessage(m store.Monitor, b store.Beat, since time.Time) Message {
	msg := n.base(m, b.At)
	var lat *float64
	if b.Latency >= 0 {
		v := math.Round(float64(b.Latency)/1e4) / 100
		lat = &v
	}
	msg.Heartbeat = &BeatInfo{Status: b.Status, At: b.At.UTC(), LatencyMS: lat, Message: b.Message}
	took := ""
	if !since.IsZero() && b.At.After(since) {
		took = Duration(b.At.Sub(since))
	}
	switch {
	case b.Status == "up":
		msg.Event, msg.Title, msg.Priority = EventUp, m.Name+" is up", 3
		msg.Text = b.Message
		if took != "" {
			msg.Text = fmt.Sprintf("Up again after %s down. %s", took, b.Message)
		}
	case b.Important:
		msg.Event, msg.Title, msg.Priority = EventDown, m.Name+" is down", 4
		msg.Text = b.Message
	default:
		msg.Event, msg.Title, msg.Priority = EventStillDown, m.Name+" is still down", 4
		msg.Text = b.Message
		if took != "" {
			msg.Text = fmt.Sprintf("Down for %s. %s", took, b.Message)
		}
	}
	return msg
}

// CertExpiring queues a certificate warning for the channels a monitor
// uses: days whole days are left until expires.
func (n *Notifier) CertExpiring(m store.Monitor, expires time.Time, days int) {
	n.enqueue(job{m.ID, n.certMessage(m, expires, days)})
}

func (n *Notifier) certMessage(m store.Monitor, expires time.Time, days int) Message {
	msg := n.base(m, time.Now())
	msg.Event, msg.Priority = EventCert, 3
	if days <= 7 {
		msg.Priority = 4
	}
	e := expires.UTC()
	msg.CertExpires = &e
	switch {
	case days < 0:
		msg.Title = m.Name + ": the certificate has expired"
	case days == 1:
		msg.Title = m.Name + ": the certificate expires in 1 day"
	default:
		msg.Title = m.Name + ": the certificate expires in " + strconv.Itoa(days) + " days"
	}
	msg.Text = "The TLS certificate expires on " + e.Format("2 January 2006 at 15:04 UTC") + ". Renew it before then."
	return msg
}

func (n *Notifier) base(m store.Monitor, at time.Time) Message {
	msg := Message{Time: at.UTC(), Monitor: &MonitorInfo{ID: m.ID, Name: m.Name, Type: m.Type, Description: m.Description}}
	if n.BaseURL != "" {
		msg.URL = n.BaseURL + "/monitors/" + strconv.FormatInt(m.ID, 10)
	}
	return msg
}

// Duration says how long something took, in at most two units: "45 s",
// "12 min", "3 h 5 min", "2 d 4 h".
func Duration(d time.Duration) string {
	s := int64(d / time.Second)
	switch {
	case s < 60:
		return fmt.Sprintf("%d s", s)
	case s < 3600:
		return fmt.Sprintf("%d min", s/60)
	case s < 86400:
		if m := s % 3600 / 60; m > 0 {
			return fmt.Sprintf("%d h %d min", s/3600, m)
		}
		return fmt.Sprintf("%d h", s/3600)
	}
	if h := s % 86400 / 3600; h > 0 {
		return fmt.Sprintf("%d d %d h", s/86400, h)
	}
	return fmt.Sprintf("%d d", s/86400)
}
