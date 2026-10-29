// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"net/http"
	"strconv"
)

// changeView is a status change as the API shows it: a heartbeat with its ID.
type changeView struct {
	ID int64 `json:"id"`
	beatView
}

// changes answers every monitor's status changes, newest first: ?limit= of
// them (default 100, most 1000), with an ID below ?before= when set.
func (s *Server) changes(w http.ResponseWriter, r *http.Request) {
	before, limit := int64(0), 100
	for name, p := range map[string]*int64{"before": &before, "limit": nil} {
		v := r.URL.Query().Get(name)
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
			limit = int(min(n, 1000))
		}
	}
	changes, err := s.Store.Changes(r.Context(), before, limit)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []changeView{}
	for _, c := range changes {
		out = append(out, changeView{c.ID, viewBeat(c.Beat)})
	}
	writeJSON(w, http.StatusOK, out)
}
