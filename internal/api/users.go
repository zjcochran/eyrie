// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/zjcochran/eyrie/internal/store"
)

// pathID reads the {id} in the path, answering 404 when it is not a number.
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		invalid(w, http.StatusNotFound, "Not found.")
	}
	return id, err == nil
}

// storeError answers an error from the store: 404 for something missing,
// 422 with the message for something not allowed, 500 for the rest.
func (s *Server) storeError(w http.ResponseWriter, r *http.Request, err error) {
	var bad *store.InvalidError
	switch {
	case errors.Is(err, store.ErrNotFound):
		invalid(w, http.StatusNotFound, "Not found.")
	case errors.As(err, &bad):
		invalid(w, http.StatusUnprocessableEntity, capitalize(bad.Msg)+".")
	default:
		s.fail(w, r, err)
	}
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	o, err := s.ssoSettings(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	users, err := s.Store.Users(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []userView{}
	for _, u := range users {
		out = append(out, viewUser(u, o))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		credentials
		Role string `json:"role"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	u, err := s.Store.CreateUser(r.Context(), body.Username, body.Password, body.Role, s.now())
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	s.Log.Info("user created", "user", u.Username, "role", u.Role, "by", sessionFrom(r).User.Username)
	o, err := s.ssoSettings(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, viewUser(u, o))
}

// updateUser changes another user's role or whether they are disabled.
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	me := sessionFrom(r).User
	if id == me.ID {
		invalid(w, http.StatusUnprocessableEntity, "You cannot change your own role or disable yourself.")
		return
	}
	ctx := r.Context()
	u, err := s.Store.UserByID(ctx, id)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	if body.Role != nil {
		o, err := s.ssoSettings(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if viewUser(u, o).RoleFromGroup {
			invalid(w, http.StatusUnprocessableEntity, "The single sign-on admin group decides the role of "+u.Username+".")
			return
		}
		if err := s.Store.SetUserRole(ctx, id, *body.Role); err != nil {
			s.storeError(w, r, err)
			return
		}
		s.Log.Info("role changed", "user", u.Username, "role", *body.Role, "by", me.Username)
	}
	if body.Disabled != nil {
		if err := s.Store.SetUserDisabled(ctx, id, *body.Disabled); err != nil {
			s.storeError(w, r, err)
			return
		}
		s.Log.Info("user disabled", "user", u.Username, "disabled", *body.Disabled, "by", me.Username)
	}
	s.writeUser(w, r, id)
}

// resetPassword sets another user's password, ending their sessions.
func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body passwordBody
	if !readJSON(w, r, &body) {
		return
	}
	u, err := s.Store.UserByID(r.Context(), id)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	if !u.HasLocal {
		invalid(w, http.StatusUnprocessableEntity, u.Username+" signs in with single sign-on only and has no password.")
		return
	}
	if err := s.Store.SetPassword(r.Context(), id, body.Password); err != nil {
		s.storeError(w, r, err)
		return
	}
	s.Log.Info("password reset", "user", u.Username, "by", sessionFrom(r).User.Username)
	w.WriteHeader(http.StatusNoContent)
}

// resetTwoFactor turns off another user's two-factor sign-in, for a lost
// phone with no recovery codes left.
func (s *Server) resetTwoFactor(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	u, err := s.Store.UserByID(r.Context(), id)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	on, err := s.Store.DisableTwoFactor(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if on {
		s.Log.Info("two-factor sign-in off", "user", u.Username, "by", sessionFrom(r).User.Username)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	me := sessionFrom(r).User
	if id == me.ID {
		invalid(w, http.StatusUnprocessableEntity, "You cannot delete yourself.")
		return
	}
	u, err := s.Store.UserByID(r.Context(), id)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	if err := s.Store.DeleteUser(r.Context(), id); err != nil {
		s.storeError(w, r, err)
		return
	}
	s.Log.Info("user deleted", "user", u.Username, "by", me.Username)
	w.WriteHeader(http.StatusNoContent)
}

// ---- API tokens ----

type tokenView struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	CreatedAt time.Time  `json:"created_at"`
	CreatedBy string     `json:"created_by"`
	LastUsed  *time.Time `json:"last_used"` // null: never
}

func viewToken(t store.APIToken) tokenView {
	v := tokenView{ID: t.ID, Name: t.Name, CreatedAt: t.CreatedAt, CreatedBy: t.CreatedBy}
	if !t.LastUsed.IsZero() {
		used := t.LastUsed
		v.LastUsed = &used
	}
	return v
}

func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.Store.APITokens(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []tokenView{}
	for _, t := range tokens {
		out = append(out, viewToken(t))
	}
	writeJSON(w, http.StatusOK, out)
}

// newToken is the answer when a token is made: the only time it is shown.
type newToken struct {
	tokenView
	Token string `json:"token"`
}

func (s *Server) createToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	me := sessionFrom(r).User
	t, secret, err := s.Store.CreateAPIToken(r.Context(), body.Name, me.Username, s.now())
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	s.Log.Info("api token created", "name", t.Name, "by", me.Username)
	writeJSON(w, http.StatusCreated, newToken{viewToken(t), secret})
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	name, err := s.Store.DeleteAPIToken(r.Context(), id)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	s.Log.Info("api token revoked", "name", name, "by", sessionFrom(r).User.Username)
	w.WriteHeader(http.StatusNoContent)
}
