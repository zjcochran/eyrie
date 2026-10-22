// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Where a profile picture came from.
const (
	PictureUpload = "upload" // the user uploaded it
	PictureSSO    = "sso"    // fetched from the single sign-on provider
)

// Picture is a user's profile picture.
type Picture struct {
	ContentType string
	Data        []byte
	Source      string // PictureUpload or PictureSSO
	UpdatedAt   time.Time
}

// SetUserPicture stores a user's profile picture, replacing any earlier one.
// A picture from single sign-on never replaces an uploaded one; stored
// reports whether it was stored.
func (s *Store) SetUserPicture(ctx context.Context, userID int64, contentType string, data []byte, source string, now time.Time) (stored bool, err error) {
	if source != PictureUpload && source != PictureSSO {
		return false, invalidf("the picture source must be upload or sso, not %q", source)
	}
	res, err := s.db.ExecContext(ctx, s.q(`
		INSERT INTO user_pictures (user_id, content_type, data, source, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (user_id) DO UPDATE SET content_type = excluded.content_type, data = excluded.data,
			source = excluded.source, updated_at = excluded.updated_at
		WHERE excluded.source = 'upload' OR user_pictures.source = 'sso'`),
		userID, contentType, data, source, now.UnixMilli())
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// DeleteUserPicture removes a user's profile picture if it came from source.
func (s *Store) DeleteUserPicture(ctx context.Context, userID int64, source string) error {
	_, err := s.db.ExecContext(ctx, s.q(`DELETE FROM user_pictures WHERE user_id = ? AND source = ?`), userID, source)
	return err
}

// UserPicture returns a user's profile picture; ok is false if there is none.
func (s *Store) UserPicture(ctx context.Context, userID int64) (p Picture, ok bool, err error) {
	var at int64
	err = s.db.QueryRowContext(ctx, s.q(`SELECT content_type, data, source, updated_at FROM user_pictures WHERE user_id = ?`), userID).
		Scan(&p.ContentType, &p.Data, &p.Source, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return Picture{}, false, nil
	}
	if err != nil {
		return Picture{}, false, err
	}
	p.UpdatedAt = fromMillis(at)
	return p, true, nil
}

// RemoveUserPicture deletes a user's picture, whatever its source. If it came
// from single sign-on, single sign-on may no longer set one.
func (s *Store) RemoveUserPicture(ctx context.Context, userID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	if _, err := tx.ExecContext(ctx, s.q(`
		UPDATE users SET sso_picture = FALSE
		WHERE id = ? AND EXISTS (SELECT 1 FROM user_pictures WHERE user_id = ? AND source = 'sso')`), userID, userID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM user_pictures WHERE user_id = ?`), userID); err != nil {
		return err
	}
	return tx.Commit()
}

// SetSSOPicture sets whether single sign-on may set a user's picture.
func (s *Store) SetSSOPicture(ctx context.Context, userID int64, on bool) error {
	_, err := s.db.ExecContext(ctx, s.q(`UPDATE users SET sso_picture = ? WHERE id = ?`), on, userID)
	return err
}
