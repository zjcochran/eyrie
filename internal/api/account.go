// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"rsc.io/qr"

	"github.com/zjcochran/eyrie/internal/sso"
	"github.com/zjcochran/eyrie/internal/store"
	"github.com/zjcochran/eyrie/internal/totp"
)

// totpIssuer names Eyrie in authenticator apps.
const totpIssuer = "Eyrie"

type identityView struct {
	Issuer   string    `json:"issuer"`
	Email    string    `json:"email"`
	LinkedAt time.Time `json:"linked_at"`
}

type twoFactorView struct {
	// Available: the account has a password, password sign-in is allowed,
	// and no single sign-on account is linked.
	Available bool `json:"available"`
	On        bool `json:"on"`
	CodesLeft int  `json:"codes_left"` // recovery codes left
}

// accountView is the answer of GET /api/v1/account.
type accountView struct {
	User       userView       `json:"user"`
	Identities []identityView `json:"identities"`
	TwoFactor  twoFactorView  `json:"two_factor"`
	SSO        ssoButton      `json:"sso"`
}

func (s *Server) account(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	u := sessionFrom(r).User
	o, err := s.ssoSettings(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	v := accountView{User: viewUser(u, o), Identities: []identityView{}, SSO: ssoButton{o.Enabled, o.Name()},
		TwoFactor: s.twoFactor(u, o)}
	ids, err := s.Store.Identities(ctx, u.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for _, i := range ids {
		v.Identities = append(v.Identities, identityView{i.Issuer, i.Email, i.LinkedAt})
	}
	if v.TwoFactor.On {
		if v.TwoFactor.CodesLeft, err = s.Store.RecoveryCodesLeft(ctx, u.ID); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) twoFactor(u store.User, o sso.Settings) twoFactorView {
	return twoFactorView{Available: u.HasLocal && !u.HasSSO && s.localAllowed(o), On: u.TwoFactor}
}

// changePassword changes the signed-in user's password. Every session of
// theirs ends; this browser gets a new one, in the answer.
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	u := sessionFrom(r).User
	if !u.HasLocal {
		invalid(w, http.StatusUnprocessableEntity, "This account signs in with single sign-on only and has no password.")
		return
	}
	if !s.confirmPassword(w, r, body.Current) {
		return
	}
	if err := s.Store.SetPassword(r.Context(), u.ID, body.New); err != nil {
		s.storeError(w, r, err)
		return
	}
	s.audit(r, "password changed", "user", u.Username)
	sess, err := s.startSession(w, r, u)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeSession(w, r, http.StatusOK, sess)
}

// confirmPassword checks the signed-in user's current password, sent to
// confirm a change.
func (s *Server) confirmPassword(w http.ResponseWriter, r *http.Request, password string) bool {
	ok, err := s.Store.VerifyPassword(r.Context(), sessionFrom(r).User.ID, password)
	if err != nil {
		s.fail(w, r, err)
		return false
	}
	if !ok {
		invalid(w, http.StatusUnprocessableEntity, "Your current password is not right.")
	}
	return ok
}

// ---- two-factor sign-in ----

func (s *Server) twoFactorFor(w http.ResponseWriter, r *http.Request) (twoFactorView, bool) {
	o, err := s.ssoSettings(r)
	if err != nil {
		s.fail(w, r, err)
		return twoFactorView{}, false
	}
	return s.twoFactor(sessionFrom(r).User, o), true
}

// twoFactorSetup is the answer of POST /api/v1/account/two-factor/setup.
type twoFactorSetup struct {
	Secret string `json:"secret"` // to type into the app
	URI    string `json:"uri"`    // the otpauth:// link in the QR code
	QR     string `json:"qr"`     // the QR code, as a data: URL of a PNG
	Sealed string `json:"sealed"` // send back with the first code
}

// startTwoFactor makes a new secret and its QR code. Nothing is stored
// until enableTwoFactor gets a right code: the secret travels sealed.
func (s *Server) startTwoFactor(w http.ResponseWriter, r *http.Request) {
	v, ok := s.twoFactorFor(w, r)
	if !ok {
		return
	}
	if !v.Available || v.On {
		invalid(w, http.StatusUnprocessableEntity, "Two-factor sign-in cannot be set up for this account.")
		return
	}
	secret := totp.NewSecret()
	sealed, err := s.Secrets.Seal(secret)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	uri := totp.URI(totpIssuer, sessionFrom(r).User.Username, secret)
	code, err := qr.Encode(uri, qr.M)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	code.Scale = 4
	writeJSON(w, http.StatusOK, twoFactorSetup{Secret: secret, URI: uri, Sealed: sealed,
		QR: "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG())})
}

// recoveryCodes is the answer when recovery codes are made: shown once.
type recoveryCodes struct {
	Codes []string `json:"codes"`
}

func (s *Server) enableTwoFactor(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Sealed   string `json:"sealed"`
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	v, ok := s.twoFactorFor(w, r)
	if !ok {
		return
	}
	if !v.Available || v.On {
		invalid(w, http.StatusUnprocessableEntity, "Two-factor sign-in cannot be set up for this account.")
		return
	}
	if !s.confirmPassword(w, r, body.Password) {
		return
	}
	secret, err := s.Secrets.Open(body.Sealed)
	if err != nil || secret == "" {
		invalid(w, http.StatusUnprocessableEntity, "The setup has expired. Start again.")
		return
	}
	step, ok := totp.Verify(secret, body.Code, s.now())
	if !ok {
		invalid(w, http.StatusUnprocessableEntity, "That code is not right. Check the time on your phone and try the next code.")
		return
	}
	codes, hashes := store.NewRecoveryCodes()
	me := sessionFrom(r).User
	if err := s.Store.EnableTwoFactor(r.Context(), me.ID, body.Sealed, step, hashes); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "two-factor sign-in on", "user", me.Username)
	writeJSON(w, http.StatusOK, recoveryCodes{codes})
}

type passwordBody struct {
	Password string `json:"password"`
}

func (s *Server) newRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	var body passwordBody
	if !readJSON(w, r, &body) {
		return
	}
	me := sessionFrom(r).User
	if !me.TwoFactor {
		invalid(w, http.StatusUnprocessableEntity, "Two-factor sign-in is off.")
		return
	}
	if !s.confirmPassword(w, r, body.Password) {
		return
	}
	codes, hashes := store.NewRecoveryCodes()
	if err := s.Store.ReplaceRecoveryCodes(r.Context(), me.ID, hashes); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "new recovery codes", "user", me.Username)
	writeJSON(w, http.StatusOK, recoveryCodes{codes})
}

func (s *Server) disableTwoFactor(w http.ResponseWriter, r *http.Request) {
	var body passwordBody
	if !readJSON(w, r, &body) {
		return
	}
	if !s.confirmPassword(w, r, body.Password) {
		return
	}
	me := sessionFrom(r).User
	on, err := s.Store.DisableTwoFactor(r.Context(), me.ID)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if on {
		s.audit(r, "two-factor sign-in off", "user", me.Username, "by", me.Username)
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- profile pictures ----

// MaxPictureBytes is the largest profile picture accepted, uploaded or
// fetched.
const MaxPictureBytes = 1 << 20

// pictureFetchTimeout bounds fetching a picture from the provider.
const pictureFetchTimeout = 10 * time.Second

// pictureTypes are the image types accepted, by sniffed content type. SVG is
// not one: served from Eyrie's origin it could run script.
var pictureTypes = map[string]bool{"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true}

var errPictureTooBig = fmt.Errorf("the picture is larger than %d KB", MaxPictureBytes/1024)

// readPicture reads an image of at most MaxPictureBytes and returns its
// sniffed content type.
func readPicture(r io.Reader) (contentType string, data []byte, err error) {
	data, err = io.ReadAll(io.LimitReader(r, MaxPictureBytes+1))
	switch {
	case err != nil:
		return "", nil, err
	case len(data) > MaxPictureBytes:
		return "", nil, errPictureTooBig
	}
	contentType = http.DetectContentType(data)
	if !pictureTypes[contentType] {
		return "", nil, errors.New("the picture must be a PNG, JPEG, GIF or WebP image")
	}
	return contentType, data, nil
}

// uploadPicture sets the signed-in user's picture from the request body,
// the image itself.
func (s *Server) uploadPicture(w http.ResponseWriter, r *http.Request) {
	ct, data, err := readPicture(r.Body)
	if err != nil {
		invalid(w, http.StatusUnprocessableEntity, capitalize(err.Error())+".")
		return
	}
	me := sessionFrom(r).User
	if _, err := s.Store.SetUserPicture(r.Context(), me.ID, ct, data, store.PictureUpload, s.now()); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeUser(w, r, me.ID)
}

// removePicture removes the signed-in user's picture. Removing one from
// single sign-on also stops single sign-on from setting it again.
func (s *Server) removePicture(w http.ResponseWriter, r *http.Request) {
	me := sessionFrom(r).User
	if err := s.Store.RemoveUserPicture(r.Context(), me.ID); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeUser(w, r, me.ID)
}

// useSSOPicture lets single sign-on set the signed-in user's picture again,
// from their next single sign-on sign-in.
func (s *Server) useSSOPicture(w http.ResponseWriter, r *http.Request) {
	me := sessionFrom(r).User
	if err := s.Store.SetSSOPicture(r.Context(), me.ID, true); err != nil {
		s.fail(w, r, err)
		return
	}
	s.writeUser(w, r, me.ID)
}

// writeUser answers with a user as it is now.
func (s *Server) writeUser(w http.ResponseWriter, r *http.Request, id int64) {
	u, err := s.Store.UserByID(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	o, err := s.ssoSettings(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, viewUser(u, o))
}

// userPicture serves a profile picture to its owner and to admins.
func (s *Server) userPicture(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	me := sessionFrom(r).User
	if err != nil || (id != me.ID && !me.IsAdmin()) {
		invalid(w, http.StatusNotFound, "No such picture.")
		return
	}
	p, ok, err := s.Store.UserPicture(r.Context(), id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if !ok {
		invalid(w, http.StatusNotFound, "No such picture.")
		return
	}
	w.Header().Set("Content-Type", p.ContentType)
	// Links carry ?v=<time stored>, so a new picture is a new URL.
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Write(p.Data) //nolint:errcheck,gosec // nothing to do if the client went away
}

// refreshSSOPicture stores the picture from the provider's picture claim,
// or drops the stored provider picture if the claim is empty. An uploaded
// picture is kept either way. Failures are logged: the sign-in goes on.
func (s *Server) refreshSSOPicture(ctx context.Context, userID int64, l sso.Login) {
	if err := s.fetchSSOPicture(ctx, userID, l); err != nil {
		s.Log.Warn("cannot fetch the single sign-on profile picture", "user_id", userID, "err", err)
	}
}

func (s *Server) fetchSSOPicture(ctx context.Context, userID int64, l sso.Login) error {
	u, err := s.Store.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if !u.SSOPicture {
		return nil // the user removed it
	}
	if l.Claims.Picture == "" {
		return s.Store.DeleteUserPicture(ctx, userID, store.PictureSSO)
	}
	pu, err := url.Parse(l.Claims.Picture)
	if err != nil || (pu.Scheme != "https" && pu.Scheme != "http") || pu.Host == "" {
		return errors.New("the picture claim is not an http(s) URL")
	}
	ctx, cancel := context.WithTimeout(ctx, pictureFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pu.String(), nil)
	if err != nil {
		return err
	}
	// The provider's own picture URLs may need the access token; it is never
	// sent anywhere else.
	if iss, err := url.Parse(l.Issuer); err == nil && iss.Scheme == pu.Scheme && iss.Host == pu.Host && l.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+l.AccessToken)
	}
	resp, err := s.HTTP.Do(req) //nolint:gosec // G704: the URL comes from the provider's signed ID token
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %s", resp.Status)
	}
	ct, data, err := readPicture(resp.Body)
	if err != nil {
		return err
	}
	_, err = s.Store.SetUserPicture(ctx, userID, ct, data, store.PictureSSO, s.now())
	return err
}
