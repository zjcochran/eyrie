// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/argon2"
)

// User roles.
const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
)

// MinPasswordLength is the minimum length of a password.
const MinPasswordLength = 12

// MaxUsernameLength is the longest username, in characters.
const MaxUsernameLength = 64

// ErrBadLogin is Authenticate's answer for an unknown user, a wrong password
// or a disabled account alike, so a sign-in reveals none of them.
var ErrBadLogin = errors.New("wrong username or password")

// ErrNotFound is returned for a user or token that does not exist.
var ErrNotFound = errors.New("not found")

// InvalidError is an error in what was asked, not in the database: a
// username that is taken, a password too short, the last admin. Its message
// is meant for people.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

func invalidf(format string, args ...any) error { return &InvalidError{fmt.Sprintf(format, args...)} }

// User is an account; the password hash stays in the database.
type User struct {
	ID         int64
	Username   string
	Role       string
	Disabled   bool
	HasLocal   bool      // has a password
	HasSSO     bool      // has a linked single sign-on account
	TwoFactor  bool      // a code is needed after the password
	SSOPicture bool      // single sign-on may set the picture
	Picture    time.Time // when the profile picture was stored; zero: none
	CreatedAt  time.Time
	LastLogin  time.Time // zero: never
}

// IsAdmin reports whether the user has the admin role.
func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

// argon2id parameters (RFC 9106's second recommended option).
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
)

// HashPassword returns an encoded argon2id hash.
func HashPassword(pw string) (string, error) {
	if len([]rune(pw)) < MinPasswordLength {
		return "", invalidf("the password must be at least %d characters", MinPasswordLength)
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads, b64(salt), b64(key)), nil
}

// CheckPassword reports whether pw matches an encoded argon2id hash.
func CheckPassword(encoded, pw string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 || len(want) > 1024 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want))) //nolint:gosec // G115: len(want) is at most 1024, checked above
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash keeps a failed sign-in for an unknown user as slow as a real one.
var dummyHash, _ = HashPassword("not-a-real-password-at-all")

// CleanUsername trims a username and checks it: 1 to MaxUsernameLength
// characters, no control characters.
func CleanUsername(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", invalidf("the username is required")
	}
	if len([]rune(name)) > MaxUsernameLength {
		return "", invalidf("the username must be at most %d characters", MaxUsernameLength)
	}
	if strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", invalidf("the username must not contain control characters")
	}
	return name, nil
}

// usernameKey is what makes usernames unique: Alice and alice are one account.
func usernameKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

func checkRole(role string) error {
	if role != RoleAdmin && role != RoleViewer {
		return invalidf("the role must be admin or viewer, not %q", role)
	}
	return nil
}

// isUnique reports whether err is a unique-constraint violation, in either
// database.
func isUnique(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint") || strings.Contains(msg, "SQLSTATE 23505")
}

// CountUsers returns the number of users; 0 means setup has not run.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	return n, s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
}

// CreateUser adds a user with a password.
func (s *Store) CreateUser(ctx context.Context, username, password, role string, now time.Time) (User, error) {
	username, err := CleanUsername(username)
	if err != nil {
		return User{}, err
	}
	if err := checkRole(role); err != nil {
		return User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	var id int64
	err = s.db.QueryRowContext(ctx, s.q(`
		INSERT INTO users (username, username_key, password_hash, role, created_at) VALUES (?, ?, ?, ?, ?)
		RETURNING id`),
		username, usernameKey(username), hash, role, now.UnixMilli()).Scan(&id)
	if isUnique(err) {
		return User{}, invalidf("the user %q already exists", username)
	}
	if err != nil {
		return User{}, err
	}
	return s.UserByID(ctx, id)
}

// CreateFirstAdmin adds the first user, an admin, only while there is none:
// two setups racing cannot both succeed.
func (s *Store) CreateFirstAdmin(ctx context.Context, username, password string, now time.Time) (User, error) {
	username, err := CleanUsername(username)
	if err != nil {
		return User{}, err
	}
	hash, err := HashPassword(password)
	if err != nil {
		return User{}, err
	}
	var id int64
	err = s.db.QueryRowContext(ctx, s.q(`
		INSERT INTO users (username, username_key, password_hash, role, created_at)
		SELECT CAST(? AS TEXT), CAST(? AS TEXT), CAST(? AS TEXT), 'admin', CAST(? AS BIGINT)
		WHERE NOT EXISTS (SELECT 1 FROM users)
		RETURNING id`),
		username, usernameKey(username), hash, now.UnixMilli()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrSetupDone
	}
	if err != nil {
		return User{}, err
	}
	return s.UserByID(ctx, id)
}

// ErrSetupDone is CreateFirstAdmin's answer once a user exists.
var ErrSetupDone = errors.New("setup is already done")

// Authenticate checks a password sign-in. The sign-in time is recorded
// here unless the user has two-factor sign-in: then TouchLogin records it
// once the code is right.
func (s *Store) Authenticate(ctx context.Context, username, password string, now time.Time) (User, error) {
	var id int64
	var hash sql.NullString
	var disabled, twoFactor bool
	err := s.db.QueryRowContext(ctx, s.q(`SELECT id, password_hash, disabled, totp_secret <> '' FROM users WHERE username_key = ?`),
		usernameKey(username)).Scan(&id, &hash, &disabled, &twoFactor)
	if errors.Is(err, sql.ErrNoRows) {
		CheckPassword(dummyHash, password)
		return User{}, ErrBadLogin
	}
	if err != nil {
		return User{}, err
	}
	if !hash.Valid || !CheckPassword(hash.String, password) || disabled {
		return User{}, ErrBadLogin
	}
	if !twoFactor {
		if err := s.TouchLogin(ctx, id, now); err != nil {
			return User{}, err
		}
	}
	return s.UserByID(ctx, id)
}

// TouchLogin records a successful sign-in.
func (s *Store) TouchLogin(ctx context.Context, id int64, now time.Time) error {
	_, err := s.db.ExecContext(ctx, s.q(`UPDATE users SET last_login = ? WHERE id = ?`), now.UnixMilli(), id)
	return err
}

const userCols = `id, username, role, disabled, password_hash IS NOT NULL,
	EXISTS (SELECT 1 FROM oidc_identities o WHERE o.user_id = users.id), totp_secret <> '', sso_picture,
	COALESCE((SELECT updated_at FROM user_pictures p WHERE p.user_id = users.id), 0), created_at, last_login`

func scanUser(sc interface{ Scan(...any) error }) (User, error) {
	var u User
	var picture, created, last int64
	err := sc.Scan(&u.ID, &u.Username, &u.Role, &u.Disabled, &u.HasLocal, &u.HasSSO, &u.TwoFactor, &u.SSOPicture,
		&picture, &created, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	u.Picture, u.CreatedAt, u.LastLogin = fromMillis(picture), fromMillis(created), fromMillis(last)
	return u, nil
}

// fromMillis turns stored Unix milliseconds into a time; 0 is the zero time.
func fromMillis(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// UserByID returns the user with this ID, or ErrNotFound.
func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, s.q(`SELECT `+userCols+` FROM users WHERE id = ?`), id))
}

// UserByName returns the user with this username, in any case, or ErrNotFound.
func (s *Store) UserByName(ctx context.Context, username string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, s.q(`SELECT `+userCols+` FROM users WHERE username_key = ?`), usernameKey(username)))
}

// Users returns every user, by username.
func (s *Store) Users(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY username_key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetPassword replaces a user's password and ends their sessions.
func (s *Store) SetPassword(ctx context.Context, id int64, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	res, err := tx.ExecContext(ctx, s.q(`UPDATE users SET password_hash = ? WHERE id = ?`), hash, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM sessions WHERE user_id = ?`), id); err != nil {
		return err
	}
	return tx.Commit()
}

// VerifyPassword checks a user's current password, to confirm a change.
func (s *Store) VerifyPassword(ctx context.Context, id int64, password string) (bool, error) {
	var hash sql.NullString
	err := s.db.QueryRowContext(ctx, s.q(`SELECT password_hash FROM users WHERE id = ?`), id).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	return hash.Valid && CheckPassword(hash.String, password), nil
}

// otherAdmins counts the enabled admins other than id.
func (s *Store) otherAdmins(ctx context.Context, tx *sql.Tx, id int64) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM users WHERE role = 'admin' AND disabled = FALSE AND id <> ?`), id).Scan(&n)
	return n, err
}

// SetUserRole changes a user's role. The last enabled admin cannot become a
// viewer.
func (s *Store) SetUserRole(ctx context.Context, id int64, role string) error {
	if err := checkRole(role); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	if role == RoleViewer {
		others, err := s.otherAdmins(ctx, tx, id)
		if err != nil {
			return err
		}
		if others == 0 {
			return invalidf("the last admin cannot become a viewer")
		}
	}
	res, err := tx.ExecContext(ctx, s.q(`UPDATE users SET role = ? WHERE id = ?`), role, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// SetUserDisabled enables or disables a user; disabling ends their sessions.
// The last enabled admin cannot be disabled.
func (s *Store) SetUserDisabled(ctx context.Context, id int64, disabled bool) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	var role string
	if err := tx.QueryRowContext(ctx, s.q(`SELECT role FROM users WHERE id = ?`), id).Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if disabled {
		others, err := s.otherAdmins(ctx, tx, id)
		if err != nil {
			return err
		}
		if role == RoleAdmin && others == 0 {
			return invalidf("the last admin cannot be disabled")
		}
		if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM sessions WHERE user_id = ?`), id); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, s.q(`UPDATE users SET disabled = ? WHERE id = ?`), disabled, id); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteUser deletes a user with their sessions, single sign-on links,
// recovery codes and picture. The last enabled admin cannot be deleted.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	var role string
	var disabled bool
	if err := tx.QueryRowContext(ctx, s.q(`SELECT role, disabled FROM users WHERE id = ?`), id).Scan(&role, &disabled); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	others, err := s.otherAdmins(ctx, tx, id)
	if err != nil {
		return err
	}
	if role == RoleAdmin && !disabled && others == 0 {
		return invalidf("the last admin cannot be deleted")
	}
	// Everything else goes with it: ON DELETE CASCADE.
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM users WHERE id = ?`), id); err != nil {
		return err
	}
	return tx.Commit()
}

// Session is a signed-in browser.
type Session struct {
	User User
	CSRF string
}

// SessionTTL is how long a session lasts without activity.
const SessionTTL = 7 * 24 * time.Hour

// CreateSession returns a new random cookie token; only its hash is stored.
func (s *Store) CreateSession(ctx context.Context, userID int64, ip, userAgent string, now time.Time) (string, error) {
	token := randomToken()
	_, err := s.db.ExecContext(ctx, s.q(`
		INSERT INTO sessions (id, user_id, csrf, created_at, expires_at, ip, user_agent) VALUES (?, ?, ?, ?, ?, ?, ?)`),
		hashToken(token), userID, randomToken(), now.UnixMilli(), now.Add(SessionTTL).UnixMilli(), ip, truncate(userAgent, 256))
	return token, err
}

// SessionByToken returns the session for a cookie token and slides its
// expiry, at most once an hour. ok is false for an unknown or expired
// session, and for a disabled user.
func (s *Store) SessionByToken(ctx context.Context, token string, now time.Time) (sess Session, ok bool, err error) {
	var uid, expires int64
	id := hashToken(token)
	err = s.db.QueryRowContext(ctx, s.q(`SELECT user_id, csrf, expires_at FROM sessions WHERE id = ?`), id).
		Scan(&uid, &sess.CSRF, &expires)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && expires < now.UnixMilli()) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	if sess.User, err = s.UserByID(ctx, uid); err != nil {
		return Session{}, false, err
	}
	if sess.User.Disabled {
		return Session{}, false, nil
	}
	if expires-SessionTTL.Milliseconds()+time.Hour.Milliseconds() < now.UnixMilli() {
		_, err = s.db.ExecContext(ctx, s.q(`UPDATE sessions SET expires_at = ? WHERE id = ?`), now.Add(SessionTTL).UnixMilli(), id)
	}
	return sess, err == nil, err
}

// DeleteSession ends the session with this token.
func (s *Store) DeleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, s.q(`DELETE FROM sessions WHERE id = ?`), hashToken(token))
	return err
}

// PruneSessions deletes expired sessions.
func (s *Store) PruneSessions(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, s.q(`DELETE FROM sessions WHERE expires_at < ?`), now.UnixMilli())
	return err
}

// randomToken is 32 random bytes in base64url.
func randomToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
