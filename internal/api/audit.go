// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zjcochran/eyrie/internal/store"
)

// audit logs an action and adds it to the audit log. args are key-value
// pairs as for slog: "by" (or else "user") is who did it, and the rest are
// the details. A failure to write the audit log is logged, never the
// caller's problem.
func (s *Server) audit(r *http.Request, action string, args ...any) {
	s.record(r, slog.LevelInfo, action, args...)
}

// auditWarn is audit for what went wrong, such as a failed sign-in.
func (s *Server) auditWarn(r *http.Request, action string, args ...any) {
	s.record(r, slog.LevelWarn, action, args...)
}

func (s *Server) record(r *http.Request, level slog.Level, action string, args ...any) {
	s.Log.Log(r.Context(), level, action, args...)
	e := store.AuditEntry{At: s.now(), IP: s.clientIP(r), Action: action}
	var user string
	var details []string
	for i := 0; i+1 < len(args); i += 2 {
		k, _ := args[i].(string)
		switch k {
		case "by":
			e.Actor = fmt.Sprint(args[i+1])
		case "user":
			user = fmt.Sprint(args[i+1])
			details = append(details, k+"="+detail(args[i+1]))
		case "ip":
		default:
			details = append(details, k+"="+detail(args[i+1]))
		}
	}
	if e.Actor == "" {
		e.Actor = user
	}
	// Who did it to themselves needs no user= as well.
	if e.Actor == user && len(details) > 0 && details[0] == "user="+detail(user) {
		details = details[1:]
	}
	e.Details = strings.Join(details, " ")
	if err := s.Store.AddAudit(r.Context(), e); err != nil {
		s.Log.Warn("cannot write the audit log", "action", action, "err", err)
	}
}

// detail is a value as the log shows it: quoted when it has spaces.
func detail(v any) string {
	var t string
	switch x := v.(type) {
	case time.Time:
		t = x.UTC().Format(time.RFC3339)
	case string:
		t = x
	default:
		t = fmt.Sprint(v)
	}
	if t == "" || strings.ContainsAny(t, " \"=") {
		return strconv.Quote(t)
	}
	return t
}

type auditView struct {
	ID      int64     `json:"id"`
	At      time.Time `json:"at"`
	User    string    `json:"user"`
	IP      string    `json:"ip"`
	Action  string    `json:"action"`
	Details string    `json:"details"`
}

// listAudit answers GET /api/v1/audit: the newest entries first, filtered
// by action and user, before an entry's id for older pages.
func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.AuditFilter{Action: q.Get("action"), Actor: q.Get("user")}
	for name, p := range map[string]*int64{"before": &f.BeforeID, "limit": nil} {
		v := q.Get(name)
		if v == "" {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			invalid(w, http.StatusBadRequest, fmt.Sprintf("%s must be a positive number.", capitalize(name)))
			return
		}
		if p != nil {
			*p = n
		} else {
			f.Limit = int(min(n, 1000))
		}
	}
	entries, err := s.Store.Audit(r.Context(), f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	actions, users, err := s.Store.AuditNames(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := struct {
		Entries []auditView `json:"entries"`
		Actions []string    `json:"actions"`
		Users   []string    `json:"users"`
	}{Entries: []auditView{}, Actions: actions, Users: users}
	for _, e := range entries {
		out.Entries = append(out.Entries, auditView{e.ID, e.At.UTC(), e.Actor, e.IP, e.Action, e.Details})
	}
	writeJSON(w, http.StatusOK, out)
}
