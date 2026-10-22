// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/zjcochran/eyrie/internal/sso"
	"github.com/zjcochran/eyrie/internal/store"
)

// The web app's pages that single sign-on sends the browser back to.
const (
	loginPage   = "/login"
	accountPage = "/settings/account"
)

func (s *Server) callbackURL(r *http.Request) string { return s.externalURL(r) + "/auth/oidc/callback" }

// safeNext only allows local paths, so a sign-in cannot redirect off-site.
func safeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.Contains(next, "\\") {
		return "/"
	}
	return next
}

// redirect sends the browser to a local page, with a message in its query.
func redirect(w http.ResponseWriter, r *http.Request, page, key, msg string) {
	to := page
	if msg != "" {
		to += "?" + url.Values{key: {msg}}.Encode()
	}
	http.Redirect(w, r, to, http.StatusSeeOther) //nolint:gosec // G710: pages are local paths: constants or safeNext output
}

// ssoSecret opens the saved client secret.
func (s *Server) ssoSecret(o sso.Settings) (string, error) { return s.Secrets.Open(o.ClientSecret) }

// ssoLogin starts a sign-in at the provider. With link=1, a signed-in user
// links their account instead.
func (s *Server) ssoLogin(w http.ResponseWriter, r *http.Request) {
	o, err := s.ssoSettings(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !o.Enabled {
		invalid(w, http.StatusNotFound, "Single sign-on is off.")
		return
	}
	next, link := safeNext(r.URL.Query().Get("next")), int64(0)
	if r.URL.Query().Get("link") == "1" {
		sess, err := s.loadSession(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if sess == nil {
			redirect(w, r, loginPage, "", "")
			return
		}
		next, link = accountPage, sess.User.ID
	}
	secret, err := s.ssoSecret(o)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	auth, err := s.sso.Start(r.Context(), o, secret, s.callbackURL(r), next, link)
	if err != nil {
		s.Log.Warn("cannot start a single sign-on sign-in", "err", err)
		redirect(w, r, loginPage, "error", capitalize(err.Error())+".")
		return
	}
	http.Redirect(w, r, auth, http.StatusFound) //nolint:gosec // G710: the provider's sign-in URL, from its discovery document
}

// ssoCallback finishes a sign-in: verify, then sign in, create or link.
func (s *Server) ssoCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	o, err := s.ssoSettings(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !o.Enabled {
		invalid(w, http.StatusNotFound, "Single sign-on is off.")
		return
	}
	secret, err := s.ssoSecret(o)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	l, err := s.sso.Finish(ctx, o, secret, s.callbackURL(r), r.URL.Query())
	var refused *sso.Refused
	switch {
	case errors.As(err, &refused):
		redirect(w, r, loginPage, "error", refused.Message)
		return
	case err != nil:
		s.Log.Warn("single sign-on failed", "err", err)
		redirect(w, r, loginPage, "error", capitalize(err.Error())+".")
		return
	}
	if l.LinkUser != 0 {
		s.linkAccount(w, r, o, l)
		return
	}
	u, msg, err := s.ssoAccount(ctx, o, l)
	switch {
	case err != nil:
		s.fail(w, r, err)
		return
	case msg != "":
		redirect(w, r, loginPage, "error", msg)
		return
	}
	s.refreshSSOPicture(ctx, u.ID, l)
	if err := s.Store.TouchLogin(ctx, u.ID, s.now()); err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := s.startSession(w, r, u); err != nil {
		s.fail(w, r, err)
		return
	}
	redirect(w, r, safeNext(l.Next), "", "")
}

// linkAccount links a verified identity to the signed-in user who started
// the link.
func (s *Server) linkAccount(w http.ResponseWriter, r *http.Request, o sso.Settings, l sso.Login) {
	ctx := r.Context()
	sess, err := s.loadSession(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if sess == nil || sess.User.ID != l.LinkUser {
		redirect(w, r, loginPage, "error", "Sign in again, then link your account.")
		return
	}
	if err := s.Store.LinkIdentity(ctx, l.LinkUser, l.Issuer, l.Subject, l.Claims.Email, s.now()); err != nil {
		var bad *store.InvalidError
		if !errors.As(err, &bad) {
			s.fail(w, r, err)
			return
		}
		redirect(w, r, accountPage, "error", capitalize(bad.Msg)+".")
		return
	}
	s.Log.Info("single sign-on linked", "user", sess.User.Username, "sso_user", l.Claims.Username())
	s.refreshSSOPicture(ctx, sess.User.ID, l)
	msg := o.Name() + " account linked."
	// A linked account signs in with the provider's own second factor.
	on, err := s.Store.DisableTwoFactor(ctx, sess.User.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if on {
		s.Log.Info("two-factor sign-in off", "user", sess.User.Username, "by", "sso link")
		msg += " Two-factor sign-in is now off: use " + o.Name() + "'s."
	}
	redirect(w, r, accountPage, "message", msg)
}

// ssoAccount finds the account for a verified identity, applying the admin
// group, or creates one when that is on. A message means the sign-in is
// refused.
func (s *Server) ssoAccount(ctx context.Context, o sso.Settings, l sso.Login) (store.User, string, error) {
	inAdmin := o.AdminGroup != "" && sso.InGroup(l.Claims.Groups, o.AdminGroup)
	role := store.RoleViewer
	if inAdmin {
		role = store.RoleAdmin
	}
	u, found, err := s.Store.UserByIdentity(ctx, l.Issuer, l.Subject)
	if err != nil {
		return store.User{}, "", err
	}
	switch {
	case found && u.Disabled:
		return store.User{}, "Your account is disabled.", nil
	case found:
		// Accounts made by single sign-on follow the admin group. The last
		// admin is never demoted: SetUserRole refuses.
		if !u.HasLocal && o.AdminGroup != "" && role != u.Role {
			if err := s.Store.SetUserRole(ctx, u.ID, role); err == nil {
				u.Role = role
				s.Log.Info("role changed", "user", u.Username, "role", role, "by", "sso groups")
			}
		}
		return u, "", nil
	case o.AutoProvision:
		u, err := s.Store.CreateSSOUser(ctx, l.Claims.Username(), role, l.Issuer, l.Subject, l.Claims.Email, s.now())
		if err != nil {
			return store.User{}, "", err
		}
		s.Log.Info("user created", "user", u.Username, "role", role, "by", "sso")
		return u, "", nil
	}
	return store.User{}, fmt.Sprintf("No Eyrie account is linked to your %s account. Ask an admin to add you.", o.Name()), nil
}

func (s *Server) unlinkSSO(w http.ResponseWriter, r *http.Request) {
	me := sessionFrom(r).User
	if err := s.Store.UnlinkIdentities(r.Context(), me.ID); err != nil {
		s.storeError(w, r, err)
		return
	}
	s.Log.Info("single sign-on unlinked", "user", me.Username)
	w.WriteHeader(http.StatusNoContent)
}

// ---- settings ----

// ssoSettingsView is the settings as admins see them: never the secret.
type ssoSettingsView struct {
	Enabled       bool   `json:"enabled"`
	Issuer        string `json:"issuer"`
	ClientID      string `json:"client_id"`
	HasSecret     bool   `json:"has_secret"`
	DisplayName   string `json:"display_name"`
	AutoProvision bool   `json:"auto_provision"`
	AllowedGroup  string `json:"allowed_group"`
	AdminGroup    string `json:"admin_group"`
	SSOOnly       bool   `json:"sso_only"`
	CallbackURL   string `json:"callback_url"` // to register at the provider
}

func (s *Server) ssoView(r *http.Request, o sso.Settings) ssoSettingsView {
	return ssoSettingsView{Enabled: o.Enabled, Issuer: o.Issuer, ClientID: o.ClientID, HasSecret: o.ClientSecret != "",
		DisplayName: o.DisplayName, AutoProvision: o.AutoProvision, AllowedGroup: o.AllowedGroup, AdminGroup: o.AdminGroup,
		SSOOnly: o.SSOOnly, CallbackURL: s.callbackURL(r)}
}

func (s *Server) getSSOSettings(w http.ResponseWriter, r *http.Request) {
	o, err := s.ssoSettings(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.ssoView(r, o))
}

func (s *Server) saveSSOSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ssoSettingsView
		ClientSecret string `json:"client_secret"` // "" keeps the saved one
		ClearSecret  bool   `json:"clear_secret"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	ctx := r.Context()
	old, err := s.ssoSettings(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	next := sso.Settings{
		Enabled: body.Enabled, Issuer: strings.TrimRight(strings.TrimSpace(body.Issuer), "/"),
		ClientID: strings.TrimSpace(body.ClientID), ClientSecret: old.ClientSecret,
		DisplayName: strings.TrimSpace(body.DisplayName), AutoProvision: body.AutoProvision,
		AllowedGroup: strings.TrimSpace(body.AllowedGroup), AdminGroup: strings.TrimSpace(body.AdminGroup), SSOOnly: body.SSOOnly,
	}
	if body.ClientSecret != "" {
		if next.ClientSecret, err = s.Secrets.Seal(body.ClientSecret); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if body.ClearSecret {
		next.ClientSecret = ""
	}
	if err := next.Validate(); err != nil {
		invalid(w, http.StatusUnprocessableEntity, capitalize(err.Error())+".")
		return
	}
	if next.Enabled && next.SSOOnly {
		ids, err := s.Store.Identities(ctx, sessionFrom(r).User.ID)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		if len(ids) == 0 {
			invalid(w, http.StatusUnprocessableEntity,
				"Link your own account to "+next.Name()+" before turning off password sign-in.")
			return
		}
	}
	if err := s.Store.SetSetting(ctx, sso.SettingKey, next, s.now()); err != nil {
		s.fail(w, r, err)
		return
	}
	s.sso.Forget()
	s.Log.Info("single sign-on settings saved", "enabled", next.Enabled, "sso_only", next.SSOOnly,
		"by", sessionFrom(r).User.Username)
	writeJSON(w, http.StatusOK, s.ssoView(r, next))
}

// ssoTest is the answer of POST /api/v1/settings/sso/test.
type ssoTest struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// testSSO checks that an issuer's discovery document loads, without saving.
func (s *Server) testSSO(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Issuer string `json:"issuer"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	issuer := strings.TrimRight(strings.TrimSpace(body.Issuer), "/")
	auth, err := s.sso.Test(r.Context(), issuer)
	if err != nil {
		writeJSON(w, http.StatusOK, ssoTest{false, capitalize(err.Error()) + "."})
		return
	}
	writeJSON(w, http.StatusOK, ssoTest{true, "Found " + issuer + " (sign-in at " + auth + ")."})
}
