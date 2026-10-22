// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"strings"
)

// RecoveryCodeCount is how many recovery codes a user gets at a time.
const RecoveryCodeCount = 10

// NewRecoveryCodes returns fresh codes to show the user once, such as
// "k3f9-x2pq-7hwd", and their hashes to store.
func NewRecoveryCodes() (codes, hashes []string) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789" // no 0/o, 1/l/i
	for range RecoveryCodeCount {
		b := make([]byte, 12)
		rand.Read(b)
		code := make([]byte, 0, 14)
		for i := range 12 {
			if i > 0 && i%4 == 0 {
				code = append(code, '-')
			}
			code = append(code, alphabet[int(b[i])%len(alphabet)])
		}
		codes = append(codes, string(code))
		hashes = append(hashes, hashToken(string(code)))
	}
	return codes, hashes
}

// normalizeRecoveryCode accepts a code typed with or without dashes or
// spaces, in any case.
func normalizeRecoveryCode(code string) string {
	code = strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(code))
	if len(code) != 12 {
		return code
	}
	return code[:4] + "-" + code[4:8] + "-" + code[8:]
}

// EnableTwoFactor turns on two-factor sign-in with a sealed TOTP secret,
// recording step as used, and replaces the user's recovery codes.
func (s *Store) EnableTwoFactor(ctx context.Context, id int64, sealedSecret string, step int64, codeHashes []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	res, err := tx.ExecContext(ctx, s.q(`UPDATE users SET totp_secret = ?, totp_step = ? WHERE id = ?`), sealedSecret, step, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if err := s.replaceCodes(ctx, tx, id, codeHashes); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) replaceCodes(ctx context.Context, tx *sql.Tx, id int64, hashes []string) error {
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM recovery_codes WHERE user_id = ?`), id); err != nil {
		return err
	}
	for _, h := range hashes {
		if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO recovery_codes (user_id, hash) VALUES (?, ?)`), id, h); err != nil {
			return err
		}
	}
	return nil
}

// ReplaceRecoveryCodes stores a new set of recovery codes for a user.
func (s *Store) ReplaceRecoveryCodes(ctx context.Context, id int64, codeHashes []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	if err := s.replaceCodes(ctx, tx, id, codeHashes); err != nil {
		return err
	}
	return tx.Commit()
}

// DisableTwoFactor turns two-factor sign-in off and deletes the recovery
// codes. It reports whether it was on.
func (s *Store) DisableTwoFactor(ctx context.Context, id int64) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	res, err := tx.ExecContext(ctx, s.q(`UPDATE users SET totp_secret = '', totp_step = 0 WHERE id = ? AND totp_secret <> ''`), id)
	if err != nil {
		return false, err
	}
	if err := s.replaceCodes(ctx, tx, id, nil); err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, tx.Commit()
}

// TwoFactorSecret returns a user's sealed TOTP secret ("" when off).
func (s *Store) TwoFactorSecret(ctx context.Context, id int64) (string, error) {
	var secret string
	err := s.db.QueryRowContext(ctx, s.q(`SELECT totp_secret FROM users WHERE id = ?`), id).Scan(&secret)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return secret, err
}

// UseTOTPStep records that a code for step was used. It reports false when
// that step or a later one was used already, so a code works only once,
// even with two sign-ins racing.
func (s *Store) UseTOTPStep(ctx context.Context, id, step int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE users SET totp_step = ? WHERE id = ? AND totp_step < ?`), step, id, step)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// UseRecoveryCode deletes a matching recovery code and reports whether there
// was one.
func (s *Store) UseRecoveryCode(ctx context.Context, id int64, code string) (bool, error) {
	res, err := s.db.ExecContext(ctx, s.q(`DELETE FROM recovery_codes WHERE user_id = ? AND hash = ?`),
		id, hashToken(normalizeRecoveryCode(code)))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// RecoveryCodesLeft counts a user's unused recovery codes.
func (s *Store) RecoveryCodesLeft(ctx context.Context, id int64) (int, error) {
	var n int
	return n, s.db.QueryRowContext(ctx, s.q(`SELECT COUNT(*) FROM recovery_codes WHERE user_id = ?`), id).Scan(&n)
}
