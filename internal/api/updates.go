// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/zjcochran/eyrie/internal/update"
)

// remindLater is how long "remind me later" puts off the update reminder.
const remindLater = 24 * time.Hour

// updatesView is the update settings as admins see them: never the proxy
// password.
type updatesView struct {
	Enabled          bool    `json:"enabled"`
	IntervalHours    int     `json:"interval_hours"`
	Proxy            string  `json:"proxy"`
	ProxyUser        string  `json:"proxy_user"`
	HasProxyPassword bool    `json:"has_proxy_password"`
	Notify           bool    `json:"notify"`
	Channels         []int64 `json:"channels"`
	// Current is the running version; Repo the GitHub repository checked
	// ("" when this build's source is elsewhere).
	Current string        `json:"current"`
	Repo    string        `json:"repo"`
	Status  update.Status `json:"status"`
	// Available is the latest release when it is newer than Current.
	Available *update.Release `json:"available"`
}

func (s *Server) updatesView(r *http.Request, u update.Settings) updatesView {
	return updatesView{Enabled: u.Enabled, IntervalHours: u.IntervalHours, Proxy: u.Proxy, ProxyUser: u.ProxyUser,
		HasProxyPassword: u.ProxyPassword != "", Notify: u.Notify, Channels: u.Channels, Current: s.Version,
		Repo: s.Updates.Repo, Status: s.Updates.Status(r.Context()), Available: s.Updates.Available(r.Context())}
}

func (s *Server) getUpdates(w http.ResponseWriter, r *http.Request) {
	u, err := update.LoadSettings(r.Context(), s.Store)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, s.updatesView(r, u))
}

// updatesBody is what saving the settings sends: an empty proxy_password
// keeps the saved one unless clear_proxy_password is set.
type updatesBody struct {
	Enabled            bool    `json:"enabled"`
	IntervalHours      int     `json:"interval_hours"`
	Proxy              string  `json:"proxy"`
	ProxyUser          string  `json:"proxy_user"`
	ProxyPassword      string  `json:"proxy_password"`
	ClearProxyPassword bool    `json:"clear_proxy_password"`
	Notify             bool    `json:"notify"`
	Channels           []int64 `json:"channels"`
}

func (s *Server) saveUpdates(w http.ResponseWriter, r *http.Request) {
	var b updatesBody
	if !readJSON(w, r, &b) {
		return
	}
	old, err := update.LoadSettings(r.Context(), s.Store)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	next := update.Settings{Enabled: b.Enabled, IntervalHours: b.IntervalHours, Proxy: b.Proxy, ProxyUser: b.ProxyUser,
		ProxyPassword: old.ProxyPassword, Notify: b.Notify, Channels: b.Channels}
	switch {
	case b.ClearProxyPassword:
		next.ProxyPassword = ""
	case b.ProxyPassword != "":
		if next.ProxyPassword, err = s.Secrets.Seal(b.ProxyPassword); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if err := next.Normalize(); err != nil {
		invalid(w, http.StatusUnprocessableEntity, capitalize(err.Error())+".")
		return
	}
	for _, id := range next.Channels {
		if _, err := s.Store.ChannelByID(r.Context(), id); err != nil {
			invalid(w, http.StatusUnprocessableEntity, "There is no notification channel "+strconv.FormatInt(id, 10)+".")
			return
		}
	}
	if err := s.Store.SetSetting(r.Context(), update.SettingKey, next, s.now()); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "update settings saved", "enabled", next.Enabled, "interval_hours", next.IntervalHours,
		"proxy", next.Proxy, "notify", next.Notify, "by", sessionFrom(r).User.Username)
	writeJSON(w, http.StatusOK, s.updatesView(r, next))
}

// checkUpdates checks GitHub now, with the saved settings, on or off.
func (s *Server) checkUpdates(w http.ResponseWriter, r *http.Request) {
	u, err := update.LoadSettings(r.Context(), s.Store)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.Updates.Check(r.Context(), u)
	writeJSON(w, http.StatusOK, s.updatesView(r, u))
}

// reminder is how an admin answered the update reminder.
type reminder struct {
	Version string    `json:"version"`
	Never   bool      `json:"never"` // don't remind me again about this version
	Until   time.Time `json:"until"` // remind me later: not before then
}

func reminderKey(userID int64) string { return "update_reminder." + strconv.FormatInt(userID, 10) }

// availableUpdate answers the release to remind the signed-in admin of:
// null when checks are off, nothing is newer, or they put it off.
func (s *Server) availableUpdate(w http.ResponseWriter, r *http.Request) {
	out := struct {
		Current string          `json:"current"`
		Release *update.Release `json:"release"`
	}{Current: s.Version}
	u, err := update.LoadSettings(r.Context(), s.Store)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if rel := s.Updates.Available(r.Context()); u.Enabled && rel != nil {
		var rem reminder
		if _, err := s.Store.GetSetting(r.Context(), reminderKey(sessionFrom(r).User.ID), &rem); err != nil {
			s.fail(w, r, err)
			return
		}
		if rem.Version != rel.Version || !rem.Never && !s.now().Before(rem.Until) {
			out.Release = rel
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// remindUpdate keeps the signed-in admin's answer to the reminder: later
// (in a day) or never, for that version.
func (s *Server) remindUpdate(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Version string `json:"version"`
		Choice  string `json:"choice"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	rem := reminder{Version: b.Version}
	switch b.Choice {
	case "later":
		rem.Until = s.now().Add(remindLater)
	case "never":
		rem.Never = true
	default:
		invalid(w, http.StatusUnprocessableEntity, `The choice must be "later" or "never".`)
		return
	}
	if b.Version == "" {
		invalid(w, http.StatusUnprocessableEntity, "The version is missing.")
		return
	}
	if err := s.Store.SetSetting(r.Context(), reminderKey(sessionFrom(r).User.ID), rem, s.now()); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
