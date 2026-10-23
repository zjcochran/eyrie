// SPDX-License-Identifier: AGPL-3.0-or-later

// Package state decides a monitor's status from its check results: a pure
// state machine, with no clock or I/O of its own.
//
// A failed check while up moves the monitor to pending, and it is checked
// again after the retry interval, up to Retries times, before it goes down.
// Only changes between up and down are notified, and again every
// ResendEvery checks while still down. Upside-down mode inverts results.
package state

import "time"

// Status is a monitor's status.
type Status string

// The statuses.
const (
	Unknown Status = "unknown" // not checked yet
	Up      Status = "up"
	Pending Status = "pending" // failed, retrying before it counts as down
	Down    Status = "down"
	// Maintenance: in a maintenance window. Package engine sets it; checks
	// go on but change nothing and notify nobody.
	Maintenance Status = "maintenance"
)

// Rules are a monitor's settings that the state machine follows.
type Rules struct {
	Retries     int  // failed checks retried before down; 0 goes down at once
	ResendEvery int  // while down, notify again every this many checks; 0 never
	UpsideDown  bool // a failed check counts as up and the other way round
}

// State is what the machine remembers between checks.
type State struct {
	Status     Status
	Since      time.Time // when Status started
	Failures   int       // failed checks in a row while pending
	DownChecks int       // checks since the last down notification
}

// Result is the outcome of one check.
type Result struct {
	OK      bool
	Message string
}

// Outcome is what a result led to.
type Outcome struct {
	State     State
	Status    Status // the status the check is recorded with (State.Status)
	Message   string
	Important bool // the status changed
	Notify    bool // send a notification: down, back up, or a reminder
	Retry     bool // check again after the retry interval, not the interval
}

// Next applies one check result at now.
func Next(r Rules, s State, res Result, now time.Time) Outcome {
	ok := res.OK != r.UpsideDown
	msg := res.Message
	if r.UpsideDown {
		msg = invert(res.OK, msg)
	}
	next := s
	o := Outcome{Message: msg}
	switch {
	case ok:
		next.Status, next.Failures, next.DownChecks = Up, 0, 0
		// Back up after being down is worth a notification; a first check or
		// the end of retries is not.
		o.Notify = s.Status == Down
	case s.Status == Down:
		next.DownChecks++
		if r.ResendEvery > 0 && next.DownChecks >= r.ResendEvery {
			next.DownChecks = 0
			o.Notify = true
		}
	case s.Failures < r.Retries:
		next.Status = Pending
		next.Failures = s.Failures + 1
		o.Retry = true
	default:
		next.Status, next.Failures, next.DownChecks = Down, 0, 0
		o.Notify = true
	}
	if next.Status != s.Status {
		next.Since = now
		o.Important = true
	}
	o.State, o.Status = next, next.Status
	return o
}

// Set is a status set by hand (manual monitors) or by something other than a
// check (push monitors that report down). It is notified like a check
// result, without retries.
func Set(s State, status Status, now time.Time) Outcome {
	next := State{Status: status, Since: s.Since}
	o := Outcome{Status: status}
	if status != s.Status {
		next.Since = now
		o.Important = true
		o.Notify = status == Down || (status == Up && s.Status == Down)
	}
	o.State = next
	return o
}

// invert explains a result that upside-down mode turned round.
func invert(ok bool, msg string) string {
	if ok {
		if msg == "" {
			return "Upside down: the check succeeded"
		}
		return "Upside down: the check succeeded: " + msg
	}
	if msg == "" {
		return "Upside down: the check failed"
	}
	return "Upside down: " + msg
}
