// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"strconv"

	"github.com/zjcochran/eyrie/internal/store"
)

// changeView is a status change as the API shows it: a heartbeat with its ID.
type changeView struct {
	ID int64 `json:"id"`
	beatView
}

// changes answers every monitor's status changes, newest first (see page),
// with their number in X-Total-Count.
func (s *Server) changes(w http.ResponseWriter, r *http.Request) {
	before, offset, limit, ok := page(w, r)
	if !ok {
		return
	}
	changes, err := s.Store.Changes(r.Context(), before, offset, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	total, err := s.Store.CountChanges(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []changeView{}
	for _, c := range changes {
		out = append(out, changeView{c.ID, viewBeat(c.Beat)})
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
	writeJSON(w, http.StatusOK, out)
}

// page reads a list's ?before= (an ID), ?offset= (how many to skip) and
// ?limit= (default 100, most 1000, or "all"). It answers 400 and returns
// false for a bad one.
func page(w http.ResponseWriter, r *http.Request) (before int64, offset, limit int, ok bool) {
	q := r.URL.Query()
	bad := func(msg string) (int64, int, int, bool) {
		invalid(w, http.StatusBadRequest, msg)
		return 0, 0, 0, false
	}
	if v := q.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			return bad("Before must be a positive number.")
		}
		before = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return bad("Offset must be 0 or more.")
		}
		offset = n
	}
	switch v := q.Get("limit"); v {
	case "":
		limit = 100
	case "all":
		limit = store.All
	default:
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return bad("Limit must be a positive number or all.")
		}
		limit = min(n, 1000)
	}
	return before, offset, limit, true
}
