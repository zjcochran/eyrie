// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"
)

// Tag labels monitors. Its name is unique, whatever the case.
type Tag struct {
	ID        int64
	Name      string
	Color     string // #rrggbb
	Monitors  int    // how many monitors have it
	CreatedAt time.Time
	UpdatedAt time.Time
}

// MonitorTag is a tag on a monitor, with the monitor's value for it.
type MonitorTag struct {
	TagID int64
	Value string
}

var hexColor = regexp.MustCompile(`^#[0-9a-f]{6}$`)

func cleanTag(t *Tag) error {
	t.Name, t.Color = strings.TrimSpace(t.Name), strings.ToLower(strings.TrimSpace(t.Color))
	if t.Name == "" || len([]rune(t.Name)) > 50 {
		return invalidf("the tag's name must be 1 to 50 characters")
	}
	if !hexColor.MatchString(t.Color) {
		return invalidf("the colour must be such as #1f5fbf")
	}
	return nil
}

// CreateTag saves a new tag.
func (s *Store) CreateTag(ctx context.Context, t Tag, now time.Time) (Tag, error) {
	if err := cleanTag(&t); err != nil {
		return Tag{}, err
	}
	var id int64
	err := s.db.QueryRowContext(ctx, s.q(`INSERT INTO tags (name, color, created_at, updated_at) VALUES (?, ?, ?, ?) RETURNING id`),
		t.Name, t.Color, now.UnixMilli(), now.UnixMilli()).Scan(&id)
	if isUnique(err) {
		return Tag{}, invalidf("there is already a tag %s", t.Name)
	}
	if err != nil {
		return Tag{}, err
	}
	return s.TagByID(ctx, id)
}

// UpdateTag renames or recolours a tag.
func (s *Store) UpdateTag(ctx context.Context, t Tag, now time.Time) (Tag, error) {
	if err := cleanTag(&t); err != nil {
		return Tag{}, err
	}
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE tags SET name = ?, color = ?, updated_at = ? WHERE id = ?`),
		t.Name, t.Color, now.UnixMilli(), t.ID)
	if isUnique(err) {
		return Tag{}, invalidf("there is already a tag %s", t.Name)
	}
	if err != nil {
		return Tag{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Tag{}, ErrNotFound
	}
	return s.TagByID(ctx, t.ID)
}

// DeleteTag deletes a tag, taking it off its monitors and maintenance
// windows.
func (s *Store) DeleteTag(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, s.q(`DELETE FROM tags WHERE id = ?`), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const tagCols = `t.id, t.name, t.color, (SELECT COUNT(*) FROM monitor_tags mt WHERE mt.tag_id = t.id), t.created_at, t.updated_at`

func scanTag(sc interface{ Scan(...any) error }) (Tag, error) {
	var t Tag
	var created, updated int64
	err := sc.Scan(&t.ID, &t.Name, &t.Color, &t.Monitors, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return Tag{}, ErrNotFound
	}
	if err != nil {
		return Tag{}, err
	}
	t.CreatedAt, t.UpdatedAt = fromMillis(created), fromMillis(updated)
	return t, nil
}

// TagByID returns the tag with this ID, or ErrNotFound.
func (s *Store) TagByID(ctx context.Context, id int64) (Tag, error) {
	return scanTag(s.db.QueryRowContext(ctx, s.q(`SELECT `+tagCols+` FROM tags t WHERE t.id = ?`), id))
}

// Tags returns every tag, by name.
func (s *Store) Tags(ctx context.Context) ([]Tag, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+tagCols+` FROM tags t ORDER BY LOWER(t.name), t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Tag
	for rows.Next() {
		t, err := scanTag(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// MonitorTags returns the tags of every monitor that has some, by monitor
// ID, in the order of the tags' names.
func (s *Store) MonitorTags(ctx context.Context) (map[int64][]MonitorTag, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT mt.monitor_id, mt.tag_id, mt.value FROM monitor_tags mt
		JOIN tags t ON t.id = mt.tag_id ORDER BY mt.monitor_id, LOWER(t.name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]MonitorTag{}
	for rows.Next() {
		var id int64
		var mt MonitorTag
		if err := rows.Scan(&id, &mt.TagID, &mt.Value); err != nil {
			return nil, err
		}
		out[id] = append(out[id], mt)
	}
	return out, rows.Err()
}

// SetMonitorTags replaces a monitor's tags. Values are at most 100
// characters.
func (s *Store) SetMonitorTags(ctx context.Context, monitorID int64, tags []MonitorTag) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM monitor_tags WHERE monitor_id = ?`), monitorID); err != nil {
		return err
	}
	seen := map[int64]bool{}
	for _, t := range tags {
		v := strings.TrimSpace(t.Value)
		if len([]rune(v)) > 100 {
			return invalidf("a tag's value must be at most 100 characters")
		}
		if seen[t.TagID] {
			return invalidf("tag %d is given twice", t.TagID)
		}
		seen[t.TagID] = true
		var exists bool
		if err := tx.QueryRowContext(ctx, s.q(`SELECT EXISTS (SELECT 1 FROM tags WHERE id = ?)`), t.TagID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return invalidf("there is no tag %d", t.TagID)
		}
		if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO monitor_tags (monitor_id, tag_id, value) VALUES (?, ?, ?)`),
			monitorID, t.TagID, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}
