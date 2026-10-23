// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"time"

	"github.com/zjcochran/eyrie/internal/store"
)

// tagView is a tag as the API shows it.
type tagView struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name"`
	Color    string    `json:"color"`
	Monitors int       `json:"monitors"` // how many monitors have it
	Created  time.Time `json:"created_at"`
}

func viewTag(t store.Tag) tagView {
	return tagView{ID: t.ID, Name: t.Name, Color: t.Color, Monitors: t.Monitors, Created: t.CreatedAt}
}

func (s *Server) listTags(w http.ResponseWriter, r *http.Request) {
	tags, err := s.Store.Tags(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []tagView{}
	for _, t := range tags {
		out = append(out, viewTag(t))
	}
	writeJSON(w, http.StatusOK, out)
}

type tagBody struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

func (s *Server) createTag(w http.ResponseWriter, r *http.Request) {
	var b tagBody
	if !readJSON(w, r, &b) {
		return
	}
	t, err := s.Store.CreateTag(r.Context(), store.Tag{Name: b.Name, Color: b.Color}, s.now())
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	s.Log.Info("tag created", "tag", t.Name, "by", sessionFrom(r).User.Username)
	writeJSON(w, http.StatusCreated, viewTag(t))
}

func (s *Server) updateTag(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b tagBody
	if !readJSON(w, r, &b) {
		return
	}
	t, err := s.Store.UpdateTag(r.Context(), store.Tag{ID: id, Name: b.Name, Color: b.Color}, s.now())
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	s.hub.publish("monitors", map[string]int64{"tag": id})
	s.Log.Info("tag changed", "tag", t.Name, "by", sessionFrom(r).User.Username)
	writeJSON(w, http.StatusOK, viewTag(t))
}

// deleteTag deletes a tag, taking it off monitors and maintenance windows.
func (s *Server) deleteTag(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	t, err := s.Store.TagByID(r.Context(), id)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	if err := s.Store.DeleteTag(r.Context(), id); err != nil {
		s.storeError(w, r, err)
		return
	}
	if err := s.Engine.ReloadMaintenance(r.Context()); err != nil {
		s.fail(w, r, err)
		return
	}
	s.hub.publish("monitors", map[string]int64{"tag": id})
	s.Log.Info("tag deleted", "tag", t.Name, "by", sessionFrom(r).User.Username)
	w.WriteHeader(http.StatusNoContent)
}
