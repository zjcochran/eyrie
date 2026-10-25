// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/zjcochran/eyrie/internal/maintenance"
	"github.com/zjcochran/eyrie/internal/store"
)

// windowView is a maintenance window as the API shows it. State is "on",
// "scheduled" (not on yet, Change: when it starts), "ended" (it will not
// be on again) or "paused" (turned off); Change is when an "on" window
// ends, if it does.
type windowView struct {
	ID          int64                `json:"id"`
	Title       string               `json:"title"`
	Description string               `json:"description"`
	Enabled     bool                 `json:"enabled"`
	Schedule    maintenance.Schedule `json:"schedule"`
	AllMonitors bool                 `json:"all_monitors"`
	Monitors    []int64              `json:"monitors"`
	Tags        []int64              `json:"tags"`
	State       string               `json:"state"`
	Change      *time.Time           `json:"change"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

func (s *Server) viewWindow(w store.Window) windowView {
	v := windowView{ID: w.ID, Title: w.Title, Description: w.Description, Enabled: w.Enabled, AllMonitors: w.AllMonitors,
		Monitors: w.MonitorIDs, Tags: w.TagIDs, CreatedAt: w.CreatedAt, UpdatedAt: w.UpdatedAt}
	json.Unmarshal(w.Schedule, &v.Schedule) //nolint:errcheck,gosec // saved by saveWindow, which checked it
	plan, err := maintenance.Compile(v.Schedule)
	switch {
	case err != nil:
		v.State = "ended" // a schedule that no longer compiles is never on
	case !w.Enabled:
		v.State = "paused"
	default:
		on, change := plan.At(s.now())
		v.Change = timeOrNil(change)
		switch {
		case on:
			v.State = "on"
		case change.IsZero():
			v.State = "ended"
		default:
			v.State = "scheduled"
		}
	}
	return v
}

func (s *Server) listWindows(w http.ResponseWriter, r *http.Request) {
	ws, err := s.Store.Windows(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []windowView{}
	for _, x := range ws {
		out = append(out, s.viewWindow(x))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getWindow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	x, err := s.Store.WindowByID(r.Context(), id)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.viewWindow(x))
}

// windowBody is what creating or changing a window sends. enabled left out
// is true.
type windowBody struct {
	Title       string               `json:"title"`
	Description string               `json:"description"`
	Enabled     *bool                `json:"enabled"`
	Schedule    maintenance.Schedule `json:"schedule"`
	AllMonitors bool                 `json:"all_monitors"`
	Monitors    []int64              `json:"monitors"`
	Tags        []int64              `json:"tags"`
}

// toWindow checks a body. Errors are meant for people.
func toWindow(b windowBody) (store.Window, error) {
	w := store.Window{Title: strings.TrimSpace(b.Title), Description: strings.TrimSpace(b.Description), Enabled: true,
		AllMonitors: b.AllMonitors, MonitorIDs: b.Monitors, TagIDs: b.Tags}
	if b.Enabled != nil {
		w.Enabled = *b.Enabled
	}
	if w.Title == "" || len([]rune(w.Title)) > 100 {
		return w, errors.New("the title must be 1 to 100 characters")
	}
	if len(w.Description) > 2000 {
		return w, errors.New("the description must be at most 2000 characters")
	}
	if !w.AllMonitors && len(w.MonitorIDs) == 0 && len(w.TagIDs) == 0 {
		return w, errors.New("choose what the window covers: every monitor, some monitors or some tags")
	}
	if w.AllMonitors {
		w.MonitorIDs, w.TagIDs = nil, nil
	}
	if _, err := maintenance.Compile(b.Schedule); err != nil {
		return w, err
	}
	w.Schedule, _ = json.Marshal(b.Schedule)
	return w, nil
}

func (s *Server) saveWindow(w http.ResponseWriter, r *http.Request, id int64) {
	var b windowBody
	if !readJSON(w, r, &b) {
		return
	}
	x, err := toWindow(b)
	if err != nil {
		invalid(w, http.StatusUnprocessableEntity, capitalize(err.Error())+".")
		return
	}
	code := http.StatusOK
	if id == 0 {
		x, err = s.Store.CreateWindow(r.Context(), x, s.now())
		code = http.StatusCreated
	} else {
		x.ID = id
		x, err = s.Store.UpdateWindow(r.Context(), x, s.now())
	}
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	if err := s.Engine.ReloadMaintenance(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "maintenance window saved", "window", x.Title, "enabled", x.Enabled, "by", sessionFrom(r).User.Username)
	writeJSON(w, code, s.viewWindow(x))
}

func (s *Server) createWindow(w http.ResponseWriter, r *http.Request) { s.saveWindow(w, r, 0) }

func (s *Server) updateWindow(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		s.saveWindow(w, r, id)
	}
}

func (s *Server) deleteWindow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	x, err := s.Store.WindowByID(r.Context(), id)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	if err := s.Store.DeleteWindow(r.Context(), id); err != nil {
		s.storeError(w, r, err)
		return
	}
	if err := s.Engine.ReloadMaintenance(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "maintenance window deleted", "window", x.Title, "by", sessionFrom(r).User.Username)
	w.WriteHeader(http.StatusNoContent)
}
