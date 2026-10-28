// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/zjcochran/eyrie/internal/store"
)

// groupView is a group as the API shows it.
type groupView struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	ParentID      *int64    `json:"parent_id"`
	Position      int       `json:"position"`
	Alert         string    `json:"alert"`
	Channels      []int64   `json:"channels"`
	AlertMonitors []int64   `json:"alert_monitors"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func viewGroup(g store.Group) groupView {
	v := groupView{ID: g.ID, Name: g.Name, Position: g.Position, Alert: g.Alert, Channels: g.Channels,
		AlertMonitors: g.AlertMonitors, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt}
	if g.ParentID != 0 {
		p := g.ParentID
		v.ParentID = &p
	}
	return v
}

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	gs, err := s.Store.Groups(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []groupView{}
	for _, g := range gs {
		out = append(out, viewGroup(g))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	g, err := s.Store.GroupByID(r.Context(), id)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, viewGroup(g))
}

// groupBody is what creating or changing a group sends. alert left out is
// "none".
type groupBody struct {
	Name          string  `json:"name"`
	ParentID      *int64  `json:"parent_id"`
	Alert         string  `json:"alert"`
	Channels      []int64 `json:"channels"`
	AlertMonitors []int64 `json:"alert_monitors"`
}

// toGroup checks a body. Errors are meant for people.
func toGroup(b groupBody) (store.Group, error) {
	g := store.Group{Name: strings.TrimSpace(b.Name), Alert: b.Alert, Channels: b.Channels, AlertMonitors: b.AlertMonitors}
	if g.Name == "" || len([]rune(g.Name)) > 100 {
		return g, errors.New("the name must be 1 to 100 characters")
	}
	if b.ParentID != nil {
		g.ParentID = *b.ParentID
	}
	if g.Alert == "" {
		g.Alert = store.AlertNone
	}
	if g.Alert == store.AlertSelected && len(g.AlertMonitors) == 0 {
		return g, errors.New("choose the members whose going down alerts")
	}
	if g.Alert != store.AlertSelected {
		g.AlertMonitors = nil
	}
	return g, nil
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) { s.saveGroup(w, r, 0) }

func (s *Server) updateGroup(w http.ResponseWriter, r *http.Request) {
	if id, ok := pathID(w, r); ok {
		s.saveGroup(w, r, id)
	}
}

func (s *Server) saveGroup(w http.ResponseWriter, r *http.Request, id int64) {
	var b groupBody
	if !readJSON(w, r, &b) {
		return
	}
	g, err := toGroup(b)
	if err != nil {
		invalid(w, http.StatusUnprocessableEntity, capitalize(err.Error())+".")
		return
	}
	code, action := http.StatusOK, "group changed"
	if id == 0 {
		g, err = s.Store.CreateGroup(r.Context(), g, s.now())
		code, action = http.StatusCreated, "group created"
	} else {
		g.ID = id
		g, err = s.Store.UpdateGroup(r.Context(), g, s.now())
	}
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	if !s.groupsChanged(w, r, g.ID) {
		return
	}
	s.audit(r, action, "group", g.Name, "alert", g.Alert, "by", sessionFrom(r).User.Username)
	writeJSON(w, code, viewGroup(g))
}

// deleteGroup deletes a group; what was in it moves to its parent.
func (s *Server) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	g, err := s.Store.GroupByID(r.Context(), id)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	if err := s.Store.DeleteGroup(r.Context(), id); err != nil {
		s.storeError(w, r, err)
		return
	}
	if !s.groupsChanged(w, r, id) {
		return
	}
	s.audit(r, "group deleted", "group", g.Name, "by", sessionFrom(r).User.Username)
	w.WriteHeader(http.StatusNoContent)
}

// groupsChanged tells the engine and the open pages that groups or where
// monitors are changed. It answers the error itself.
func (s *Server) groupsChanged(w http.ResponseWriter, r *http.Request, id int64) bool {
	if err := s.Engine.ReloadGroups(r.Context()); err != nil {
		s.fail(w, r, err)
		return false
	}
	s.hub.publish("monitors", map[string]int64{"group": id})
	return true
}

// layoutBody places groups and monitors: each item goes into its group (null
// for none), in the order of the list among the items with the same group.
type layoutBody struct {
	Items []struct {
		Type    string `json:"type"` // "group" or "monitor"
		ID      int64  `json:"id"`
		GroupID *int64 `json:"group_id"`
	} `json:"items"`
}

// maxLayout bounds the items one layout may place.
const maxLayout = 10000

func (s *Server) arrange(w http.ResponseWriter, r *http.Request) {
	var b layoutBody
	if !readJSON(w, r, &b) {
		return
	}
	if len(b.Items) > maxLayout {
		invalid(w, http.StatusUnprocessableEntity, "Too many items.")
		return
	}
	places := make([]store.Place, 0, len(b.Items))
	for _, it := range b.Items {
		if it.Type != "group" && it.Type != "monitor" {
			invalid(w, http.StatusUnprocessableEntity, `Each item's type must be "group" or "monitor".`)
			return
		}
		p := store.Place{Group: it.Type == "group", ID: it.ID}
		if it.GroupID != nil {
			p.GroupID = *it.GroupID
		}
		places = append(places, p)
	}
	if err := s.Store.Arrange(r.Context(), places, s.now()); err != nil {
		s.storeError(w, r, err)
		return
	}
	if !s.groupsChanged(w, r, 0) {
		return
	}
	s.audit(r, "monitors rearranged", "items", len(places), "by", sessionFrom(r).User.Username)
	w.WriteHeader(http.StatusNoContent)
}
