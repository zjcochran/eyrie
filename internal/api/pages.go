// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/zjcochran/eyrie/internal/store"
)

// pageView is a status page as signed-in users see it.
type pageView struct {
	ID             int64         `json:"id"`
	Slug           string        `json:"slug"`
	Title          string        `json:"title"`
	Description    string        `json:"description"`
	Footer         string        `json:"footer"`
	Published      bool          `json:"published"`
	Theme          string        `json:"theme"`
	Accent         string        `json:"accent"`
	ShowTags       bool          `json:"show_tags"`
	ShowCertExpiry bool          `json:"show_cert_expiry"`
	Sections       []sectionView `json:"sections"`
	LogoURL        string        `json:"logo_url"` // "" for none
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

type sectionView struct {
	Title    string  `json:"title"`
	TagID    *int64  `json:"tag_id"`
	Monitors []int64 `json:"monitors"`
}

// logoURL is where a page's logo is, with its time so a new logo is a new
// URL.
func logoURL(p store.Page) string {
	if p.LogoUpdated.IsZero() {
		return ""
	}
	return fmt.Sprintf("/api/v1/status/%s/logo?v=%d", p.Slug, p.LogoUpdated.UnixMilli())
}

func viewPage(p store.Page) pageView {
	v := pageView{ID: p.ID, Slug: p.Slug, Title: p.Title, Description: p.Description, Footer: p.Footer, Published: p.Published,
		Theme: p.Theme, Accent: p.Accent, ShowTags: p.ShowTags, ShowCertExpiry: p.ShowCertExpiry, Sections: []sectionView{},
		LogoURL: logoURL(p), CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
	for _, sec := range p.Sections {
		sv := sectionView{Title: sec.Title, Monitors: sec.MonitorIDs}
		if sec.TagID != 0 {
			id := sec.TagID
			sv.TagID = &id
		}
		v.Sections = append(v.Sections, sv)
	}
	return v
}

func (s *Server) listPages(w http.ResponseWriter, r *http.Request) {
	ps, err := s.Store.Pages(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []pageView{}
	for _, p := range ps {
		out = append(out, viewPage(p))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) pageFor(w http.ResponseWriter, r *http.Request) (store.Page, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return store.Page{}, false
	}
	p, err := s.Store.PageByID(r.Context(), id)
	if err != nil {
		s.storeError(w, r, err)
		return store.Page{}, false
	}
	return p, true
}

func (s *Server) getPage(w http.ResponseWriter, r *http.Request) {
	if p, ok := s.pageFor(w, r); ok {
		writeJSON(w, http.StatusOK, viewPage(p))
	}
}

// pageBody is what creating or changing a page sends. theme left out is
// auto.
type pageBody struct {
	Slug           string        `json:"slug"`
	Title          string        `json:"title"`
	Description    string        `json:"description"`
	Footer         string        `json:"footer"`
	Published      bool          `json:"published"`
	Theme          string        `json:"theme"`
	Accent         string        `json:"accent"`
	ShowTags       bool          `json:"show_tags"`
	ShowCertExpiry bool          `json:"show_cert_expiry"`
	Sections       []sectionView `json:"sections"`
}

func (b pageBody) page() store.Page {
	p := store.Page{Slug: b.Slug, Title: b.Title, Description: b.Description, Footer: b.Footer, Published: b.Published,
		Theme: b.Theme, Accent: b.Accent, ShowTags: b.ShowTags, ShowCertExpiry: b.ShowCertExpiry}
	if p.Theme == "" {
		p.Theme = "auto"
	}
	for _, sv := range b.Sections {
		sec := store.Section{Title: sv.Title, MonitorIDs: sv.Monitors}
		if sv.TagID != nil {
			sec.TagID = *sv.TagID
		}
		p.Sections = append(p.Sections, sec)
	}
	return p
}

func (s *Server) createPage(w http.ResponseWriter, r *http.Request) {
	var b pageBody
	if !readJSON(w, r, &b) {
		return
	}
	p, err := s.Store.CreatePage(r.Context(), b.page(), s.now())
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	s.Log.Info("status page created", "page", p.Slug, "by", sessionFrom(r).User.Username)
	writeJSON(w, http.StatusCreated, viewPage(p))
}

func (s *Server) updatePage(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b pageBody
	if !readJSON(w, r, &b) {
		return
	}
	p := b.page()
	p.ID = id
	p, err := s.Store.UpdatePage(r.Context(), p, s.now())
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	s.Log.Info("status page changed", "page", p.Slug, "published", p.Published, "by", sessionFrom(r).User.Username)
	writeJSON(w, http.StatusOK, viewPage(p))
}

func (s *Server) deletePage(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pageFor(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeletePage(r.Context(), p.ID); err != nil {
		s.storeError(w, r, err)
		return
	}
	s.Log.Info("status page deleted", "page", p.Slug, "by", sessionFrom(r).User.Username)
	w.WriteHeader(http.StatusNoContent)
}

// uploadLogo sets a page's logo from the request body, the image itself,
// with the limits of profile pictures.
func (s *Server) uploadLogo(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pageFor(w, r)
	if !ok {
		return
	}
	ct, data, err := readPicture(r.Body)
	if err != nil {
		invalid(w, http.StatusUnprocessableEntity, capitalize(err.Error())+".")
		return
	}
	if err := s.Store.SetPageLogo(r.Context(), p.ID, ct, data, s.now()); err != nil {
		s.storeError(w, r, err)
		return
	}
	s.getPage(w, r)
}

func (s *Server) removeLogo(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pageFor(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeletePageLogo(r.Context(), p.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.getPage(w, r)
}

// ---- incidents ----

// incidentView is an incident as the API shows it. Author is left out of
// the public page.
type incidentView struct {
	ID         int64        `json:"id"`
	Title      string       `json:"title"`
	Status     string       `json:"status"`
	Updates    []updateView `json:"updates"` // newest first
	CreatedAt  time.Time    `json:"created_at"`
	UpdatedAt  time.Time    `json:"updated_at"`
	ResolvedAt *time.Time   `json:"resolved_at"`
}

type updateView struct {
	Status    string    `json:"status"`
	Message   string    `json:"message"`
	Author    string    `json:"author,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func viewIncident(in store.Incident, authors bool) incidentView {
	v := incidentView{ID: in.ID, Title: in.Title, Status: in.Status, Updates: []updateView{}, CreatedAt: in.CreatedAt,
		UpdatedAt: in.UpdatedAt, ResolvedAt: timeOrNil(in.ResolvedAt)}
	for _, u := range in.Updates {
		uv := updateView{Status: u.Status, Message: u.Message, CreatedAt: u.CreatedAt}
		if authors {
			uv.Author = u.Author
		}
		v.Updates = append(v.Updates, uv)
	}
	return v
}

// listIncidents answers every incident of a page, newest first.
func (s *Server) listIncidents(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pageFor(w, r)
	if !ok {
		return
	}
	ins, err := s.Store.Incidents(r.Context(), p.ID, time.Time{})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []incidentView{}
	for _, in := range ins {
		out = append(out, viewIncident(in, true))
	}
	writeJSON(w, http.StatusOK, out)
}

type incidentBody struct {
	Title   string `json:"title"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// openIncident opens an incident on a page with its first update.
func (s *Server) openIncident(w http.ResponseWriter, r *http.Request) {
	p, ok := s.pageFor(w, r)
	if !ok {
		return
	}
	var b incidentBody
	if !readJSON(w, r, &b) {
		return
	}
	me := sessionFrom(r).User.Username
	in, err := s.Store.CreateIncident(r.Context(), p.ID, b.Title, b.Status, b.Message, me, s.now())
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	s.Log.Info("incident opened", "page", p.Slug, "incident", in.Title, "by", me)
	writeJSON(w, http.StatusCreated, viewIncident(in, true))
}

// updateIncident adds an update to an incident's timeline.
func (s *Server) updateIncident(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b incidentBody
	if !readJSON(w, r, &b) {
		return
	}
	me := sessionFrom(r).User.Username
	in, err := s.Store.AddIncidentUpdate(r.Context(), id, b.Status, b.Message, me, s.now())
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	s.Log.Info("incident updated", "incident", in.Title, "status", in.Status, "by", me)
	writeJSON(w, http.StatusOK, viewIncident(in, true))
}

func (s *Server) renameIncident(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b incidentBody
	if !readJSON(w, r, &b) {
		return
	}
	in, err := s.Store.RenameIncident(r.Context(), id, b.Title, s.now())
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, viewIncident(in, true))
}

func (s *Server) deleteIncident(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteIncident(r.Context(), id); err != nil {
		s.storeError(w, r, err)
		return
	}
	s.Log.Info("incident deleted", "incident", id, "by", sessionFrom(r).User.Username)
	w.WriteHeader(http.StatusNoContent)
}
