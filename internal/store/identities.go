// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Identity is a single sign-on account linked to a user.
type Identity struct {
	Issuer, Subject, Email string
	LinkedAt               time.Time
}

// UserByIdentity returns the user linked to issuer and subject; ok is false
// when there is none.
func (s *Store) UserByIdentity(ctx context.Context, issuer, subject string) (u User, ok bool, err error) {
	var id int64
	err = s.db.QueryRowContext(ctx, s.q(`SELECT user_id FROM oidc_identities WHERE issuer = ? AND subject = ?`),
		issuer, subject).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, err
	}
	u, err = s.UserByID(ctx, id)
	return u, err == nil, err
}

// LinkIdentity links a single sign-on account to a user. An account already
// linked to someone else is refused.
func (s *Store) LinkIdentity(ctx context.Context, userID int64, issuer, subject, email string, now time.Time) error {
	existing, ok, err := s.UserByIdentity(ctx, issuer, subject)
	if err != nil {
		return err
	}
	if ok && existing.ID != userID {
		return invalidf("this single sign-on account is already linked to %s", existing.Username)
	}
	_, err = s.db.ExecContext(ctx, s.q(`
		INSERT INTO oidc_identities (issuer, subject, user_id, email, linked_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (issuer, subject) DO UPDATE SET email = excluded.email`),
		issuer, subject, userID, email, now.UnixMilli())
	return err
}

// UnlinkIdentities removes a user's single sign-on links. A user without a
// password would be locked out, so that is refused.
func (s *Store) UnlinkIdentities(ctx context.Context, userID int64) error {
	u, err := s.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	if !u.HasLocal {
		return invalidf("set a password first: this account has no other way to sign in")
	}
	_, err = s.db.ExecContext(ctx, s.q(`DELETE FROM oidc_identities WHERE user_id = ?`), userID)
	return err
}

// Identities returns the single sign-on accounts linked to a user, oldest
// first.
func (s *Store) Identities(ctx context.Context, userID int64) ([]Identity, error) {
	rows, err := s.db.QueryContext(ctx,
		s.q(`SELECT issuer, subject, email, linked_at FROM oidc_identities WHERE user_id = ? ORDER BY linked_at, issuer, subject`), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Identity
	for rows.Next() {
		var i Identity
		var at int64
		if err := rows.Scan(&i.Issuer, &i.Subject, &i.Email, &at); err != nil {
			return nil, err
		}
		i.LinkedAt = fromMillis(at)
		out = append(out, i)
	}
	return out, rows.Err()
}

// CreateSSOUser creates a user without a password, linked to a single
// sign-on account, picking a free username based on want: want, want-2,
// want-3, ….
func (s *Store) CreateSSOUser(ctx context.Context, want, role, issuer, subject, email string, now time.Time) (User, error) {
	if err := checkRole(role); err != nil {
		return User{}, err
	}
	base := []rune(strings.TrimSpace(want))
	base = base[:min(len(base), MaxUsernameLength-4)] // room for "-100"
	name, err := CleanUsername(string(base))
	if err != nil {
		name = "user"
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	var id int64
	for n := 1; ; n++ {
		if n > 100 {
			return User{}, invalidf("no free username")
		}
		try := name
		if n > 1 {
			try = fmt.Sprintf("%s-%d", name, n)
		}
		// Parameterized: every value is a bound argument, not SQL text.
		// nosemgrep
		err := tx.QueryRowContext(ctx, s.q(`
			INSERT INTO users (username, username_key, role, created_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (username_key) DO NOTHING RETURNING id`),
			try, usernameKey(try), role, now.UnixMilli()).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			continue // taken
		}
		if err != nil {
			return User{}, err
		}
		break
	}
	if _, err := tx.ExecContext(ctx, s.q(`
		INSERT INTO oidc_identities (issuer, subject, user_id, email, linked_at) VALUES (?, ?, ?, ?, ?)`),
		issuer, subject, id, email, now.UnixMilli()); err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return s.UserByID(ctx, id)
}
