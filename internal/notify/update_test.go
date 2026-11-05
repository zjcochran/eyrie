// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestUpdate(t *testing.T) {
	n := newNotifier(t)
	on, off := newReceiver(t), newReceiver(t)
	a := link(t, n, Channel{Name: "On", Kind: KindWebhook, URL: on.srv.URL, Enabled: true})
	b := link(t, n, Channel{Name: "Off", Kind: KindWebhook, URL: off.srv.URL})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go n.Run(ctx)
	n.Update([]int64{a.ID, b.ID, 999}, "v1.0.0", "v1.2.0", "https://github.com/example/eyrie/releases/tag/v1.2.0",
		"## Changes\n\n- "+strings.Repeat("x", 2000))
	waitFor(t, "the update message", func() bool { return len(on.got()) == 1 })
	var m Message
	json.Unmarshal([]byte(on.got()[0].body), &m)
	if m.Event != EventUpdate || m.Title != "Eyrie v1.2.0 is available" || m.URL != "https://github.com/example/eyrie/releases/tag/v1.2.0" ||
		!strings.HasPrefix(m.Text, "Eyrie v1.2.0 is out; this one runs v1.0.0.\n\n## Changes") || len(m.Text) > 1600 || m.Monitor != nil {
		t.Errorf("message %+v", m)
	}
	if len(off.got()) != 0 {
		t.Error("a channel that is off got it")
	}
	if got := updateMessage("v1", "v2", "", " ", t0).Text; got != "Eyrie v2 is out; this one runs v1." {
		t.Errorf("without notes %q", got)
	}
}
