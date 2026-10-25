// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/zjcochran/eyrie/internal/notify"
	"github.com/zjcochran/eyrie/internal/store"
)

// channelView is a channel as admins see it: never its secrets. Of the URL,
// only the scheme and host are shown, since the rest can be a credential (a
// Discord webhook's path, an ntfy topic).
type channelView struct {
	ID       int64       `json:"id"`
	Name     string      `json:"name"`
	Kind     string      `json:"kind"`
	URLHost  string      `json:"url_host"`
	HasURL   bool        `json:"has_url"`
	HasToken bool        `json:"has_token"`
	HasUser  bool        `json:"has_user"`
	To       string      `json:"to"`
	From     string      `json:"from"`
	Enabled  bool        `json:"enabled"`
	Default  bool        `json:"default"`
	Monitors int         `json:"monitors"`
	Last     *resultView `json:"last"` // null: nothing sent since Eyrie started
}

type resultView struct {
	At    time.Time `json:"at"`
	Error string    `json:"error"` // "": it worked
}

func host(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func (s *Server) viewChannel(sc store.Channel) (channelView, error) {
	c, err := notify.FromStore(sc, s.Secrets)
	if err != nil {
		return channelView{}, err
	}
	v := channelView{ID: sc.ID, Name: c.Name, Kind: c.Kind, URLHost: host(c.URL), HasURL: c.URL != "", HasToken: c.Token != "",
		HasUser: c.User != "", To: c.To, From: c.From, Enabled: c.Enabled, Default: c.Default, Monitors: sc.Monitors}
	if r, ok := s.Notifier.Last(sc.ID); ok {
		v.Last = &resultView{r.At, r.Err}
	}
	return v, nil
}

func (s *Server) listChannels(w http.ResponseWriter, r *http.Request) {
	cs, err := s.Store.Channels(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := []channelView{}
	for _, c := range cs {
		v, err := s.viewChannel(c)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) channelKinds(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, notify.Kinds)
}

// channelBody is what creating, changing or testing a channel sends. On a
// saved channel, an empty URL, token or user keeps the saved one unless its
// clear_ flag is set.
type channelBody struct {
	ID         int64  `json:"id"` // tests only: fill in the saved secrets of this channel
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	URL        string `json:"url"`
	Token      string `json:"token"`
	User       string `json:"user"`
	To         string `json:"to"`
	From       string `json:"from"`
	Enabled    bool   `json:"enabled"`
	Default    bool   `json:"default"`
	ApplyToAll bool   `json:"apply_to_all"`
	ClearURL   bool   `json:"clear_url"`
	ClearToken bool   `json:"clear_token"`
	ClearUser  bool   `json:"clear_user"`
}

// channel builds a channel from a body and, when id is not 0, the saved
// channel's secrets. It answers the error itself.
func (s *Server) channel(w http.ResponseWriter, r *http.Request, b channelBody, id int64) (notify.Channel, bool) {
	c := notify.Channel{ID: id, Name: strings.TrimSpace(b.Name), Kind: b.Kind, URL: strings.TrimSpace(b.URL),
		Token: strings.TrimSpace(b.Token), User: strings.TrimSpace(b.User), To: strings.TrimSpace(b.To),
		From: strings.TrimSpace(b.From), Enabled: b.Enabled, Default: b.Default}
	if id != 0 {
		sc, err := s.Store.ChannelByID(r.Context(), id)
		if err != nil {
			s.storeError(w, r, err)
			return c, false
		}
		saved, err := notify.FromStore(sc, s.Secrets)
		if err != nil {
			s.fail(w, r, err)
			return c, false
		}
		keep := func(v *string, saved string, drop bool) {
			if *v == "" && !drop {
				*v = saved
			}
		}
		keep(&c.URL, saved.URL, b.ClearURL)
		keep(&c.Token, saved.Token, b.ClearToken)
		keep(&c.User, saved.User, b.ClearUser)
	}
	if err := c.Validate(); err != nil {
		invalid(w, http.StatusUnprocessableEntity, capitalize(err.Error())+".")
		return c, false
	}
	return c, true
}

func (s *Server) createChannel(w http.ResponseWriter, r *http.Request) {
	var b channelBody
	if !readJSON(w, r, &b) {
		return
	}
	c, ok := s.channel(w, r, b, 0)
	if !ok {
		return
	}
	s.saveChannel(w, r, c, b.ApplyToAll, http.StatusCreated)
}

func (s *Server) updateChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b channelBody
	if !readJSON(w, r, &b) {
		return
	}
	c, ok := s.channel(w, r, b, id)
	if !ok {
		return
	}
	s.saveChannel(w, r, c, b.ApplyToAll, http.StatusOK)
}

func (s *Server) saveChannel(w http.ResponseWriter, r *http.Request, c notify.Channel, toAll bool, code int) {
	sc, err := notify.ToStore(c, s.Secrets)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if c.ID == 0 {
		sc, err = s.Store.CreateChannel(r.Context(), sc, toAll, s.now())
	} else {
		sc, err = s.Store.UpdateChannel(r.Context(), sc, toAll, s.now())
	}
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	s.audit(r, "notification channel saved", "channel", sc.Name, "kind", sc.Kind, "by", sessionFrom(r).User.Username)
	if toAll {
		s.hub.publish("monitors", map[string]int64{"id": 0})
	}
	v, err := s.viewChannel(sc)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, code, v)
}

func (s *Server) deleteChannel(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	sc, err := s.Store.ChannelByID(r.Context(), id)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	if err := s.Store.DeleteChannel(r.Context(), id); err != nil {
		s.storeError(w, r, err)
		return
	}
	s.audit(r, "notification channel deleted", "channel", sc.Name, "by", sessionFrom(r).User.Username)
	w.WriteHeader(http.StatusNoContent)
}

// testChannel sends a test message with the form as it is, saved or not.
func (s *Server) testChannel(w http.ResponseWriter, r *http.Request) {
	var b channelBody
	if !readJSON(w, r, &b) {
		return
	}
	if b.Name == "" {
		b.Name = "test"
	}
	c, ok := s.channel(w, r, b, b.ID)
	if !ok {
		return
	}
	if err := s.Notifier.Test(r.Context(), c); err != nil {
		writeJSON(w, http.StatusOK, ssoTest{false, capitalize(err.Error()) + "."})
		return
	}
	writeJSON(w, http.StatusOK, ssoTest{true, "Sent."})
}

func (s *Server) getNotifySettings(w http.ResponseWriter, r *http.Request) {
	v, err := notify.LoadSettings(r.Context(), s.Store)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) saveNotifySettings(w http.ResponseWriter, r *http.Request) {
	var v notify.Settings
	if !readJSON(w, r, &v) {
		return
	}
	if err := v.Normalize(); err != nil {
		invalid(w, http.StatusUnprocessableEntity, capitalize(err.Error())+".")
		return
	}
	if err := s.Store.SetSetting(r.Context(), notify.SettingKey, v, s.now()); err != nil {
		s.fail(w, r, err)
		return
	}
	s.Notifier.SetSettings(v)
	s.audit(r, "notification settings saved", "cert_expiry_days", v.CertExpiryDays, "by", sessionFrom(r).User.Username)
	writeJSON(w, http.StatusOK, v)
}
