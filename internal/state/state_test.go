// SPDX-License-Identifier: AGPL-3.0-or-later

package state

import (
	"testing"
	"time"
)

var t0 = time.Unix(1_800_000_000, 0)

// run feeds results (true = ok) through Next and returns every outcome.
func run(r Rules, s State, results ...bool) []Outcome {
	var out []Outcome
	for i, ok := range results {
		o := Next(r, s, Result{OK: ok, Message: "m"}, t0.Add(time.Duration(i)*time.Minute))
		out = append(out, o)
		s = o.State
	}
	return out
}

func statuses(os []Outcome) string {
	b := ""
	for _, o := range os {
		c := map[Status]string{Up: "U", Down: "D", Pending: "P"}[o.Status]
		if o.Notify {
			c += "!"
		}
		if o.Retry {
			c += "r"
		}
		b += c + " "
	}
	return b
}

func TestSequences(t *testing.T) {
	for _, c := range []struct {
		name    string
		rules   Rules
		start   Status
		results []bool
		want    string
	}{
		{"first check up", Rules{}, Unknown, []bool{true}, "U "},
		{"first check down, no retries", Rules{}, Unknown, []bool{false}, "D! "},
		{"down and back", Rules{}, Up, []bool{false, false, true, true}, "D! D U! U "},
		{"retries", Rules{Retries: 2}, Up, []bool{false, false, false, false}, "Pr Pr D! D "},
		{"recovers while pending: no notification", Rules{Retries: 2}, Up, []bool{false, true, false, false, false}, "Pr U Pr Pr D! "},
		{"resend every 2 checks", Rules{ResendEvery: 2}, Up, []bool{false, false, false, false, false}, "D! D D! D D! "},
		{"resend every check", Rules{ResendEvery: 1}, Up, []bool{false, false, false}, "D! D! D! "},
		{"upside down", Rules{UpsideDown: true}, Up, []bool{true, false, false}, "D! U! U "},
		{"retries then up after down", Rules{Retries: 1, ResendEvery: 3}, Up, []bool{false, false, false, false, false, true}, "Pr D! D D D! U! "},
	} {
		got := statuses(run(c.rules, State{Status: c.start, Since: t0}, c.results...))
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSinceAndImportant(t *testing.T) {
	os := run(Rules{Retries: 1}, State{Status: Up, Since: t0}, true, false, false, false, true)
	for i, want := range []bool{false, true, true, false, true} {
		if os[i].Important != want {
			t.Errorf("check %d: important %v", i, os[i].Important)
		}
	}
	if !os[1].State.Since.Equal(t0.Add(time.Minute)) || !os[3].State.Since.Equal(t0.Add(2*time.Minute)) {
		t.Errorf("since: %v %v", os[1].State.Since, os[3].State.Since)
	}
	if os[0].State.Since != t0 {
		t.Error("since moved without a change")
	}
}

func TestUpsideDownMessages(t *testing.T) {
	r := Rules{UpsideDown: true}
	for _, c := range []struct {
		res  Result
		want string
	}{
		{Result{OK: true, Message: "200 OK"}, "Upside down: the check succeeded: 200 OK"},
		{Result{OK: true}, "Upside down: the check succeeded"},
		{Result{OK: false, Message: "timeout"}, "Upside down: timeout"},
		{Result{OK: false}, "Upside down: the check failed"},
	} {
		if got := Next(r, State{Status: Up}, c.res, t0).Message; got != c.want {
			t.Errorf("%+v: %q", c.res, got)
		}
	}
	if got := Next(Rules{}, State{}, Result{OK: true, Message: "fine"}, t0).Message; got != "fine" {
		t.Errorf("plain message %q", got)
	}
}

func TestSet(t *testing.T) {
	s := State{Status: Up, Since: t0}
	o := Set(s, Down, t0.Add(time.Minute))
	if !o.Important || !o.Notify || o.State.Status != Down || !o.State.Since.Equal(t0.Add(time.Minute)) {
		t.Errorf("down: %+v", o)
	}
	o = Set(o.State, Up, t0.Add(2*time.Minute))
	if !o.Important || !o.Notify {
		t.Errorf("back up: %+v", o)
	}
	o = Set(o.State, Up, t0.Add(3*time.Minute))
	if o.Important || o.Notify || !o.State.Since.Equal(t0.Add(2*time.Minute)) {
		t.Errorf("same status: %+v", o)
	}
	if o := Set(State{Status: Unknown}, Up, t0); !o.Important || o.Notify {
		t.Errorf("first status: %+v", o)
	}
	if o := Set(State{Status: Up}, Pending, t0); o.Notify {
		t.Errorf("pending: %+v", o)
	}
}
