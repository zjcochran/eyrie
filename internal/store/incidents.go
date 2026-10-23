// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"slices"
	"strings"
	"time"
)

// IncidentStatuses are the statuses of incidents, in the order an incident
// usually goes through them.
var IncidentStatuses = []string{"investigating", "identified", "monitoring", "resolved"}

// IncidentResolved is the status that closes an incident.
const IncidentResolved = "resolved"

// Incident is something people report on a status page, with a timeline
// of updates. Its status is that of its latest update.
type Incident struct {
	ID         int64
	PageID     int64
	Title      string
	Status     string
	Updates    []IncidentUpdate // newest first
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ResolvedAt time.Time // zero while open
}

// IncidentUpdate is one entry of an incident's timeline.
type IncidentUpdate struct {
	ID        int64
	Status    string
	Message   string
	Author    string // the username of who posted it
	CreatedAt time.Time
}

func cleanUpdate(status, message string) (string, error) {
	message = strings.TrimSpace(message)
	if !slices.Contains(IncidentStatuses, status) {
		return "", invalidf("the status must be investigating, identified, monitoring or resolved")
	}
	if message == "" || len([]rune(message)) > 5000 {
		return "", invalidf("the message must be 1 to 5000 characters")
	}
	return message, nil
}

func cleanIncidentTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" || len([]rune(title)) > 200 {
		return "", invalidf("the title must be 1 to 200 characters")
	}
	return title, nil
}

// CreateIncident opens an incident on a page with its first update.
func (s *Store) CreateIncident(ctx context.Context, pageID int64, title, status, message, author string, now time.Time) (Incident, error) {
	title, err := cleanIncidentTitle(title)
	if err != nil {
		return Incident{}, err
	}
	if message, err = cleanUpdate(status, message); err != nil {
		return Incident{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Incident{}, err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	var id int64
	err = tx.QueryRowContext(ctx, s.q(`INSERT INTO incidents (page_id, title, status, created_at, updated_at, resolved_at)
		VALUES (?, ?, ?, ?, ?, ?) RETURNING id`), pageID, title, status, now.UnixMilli(), now.UnixMilli(), resolvedAt(status, now)).Scan(&id)
	if isForeignKey(err) {
		return Incident{}, ErrNotFound
	}
	if err != nil {
		return Incident{}, err
	}
	if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO incident_updates (incident_id, status, message, author, created_at)
		VALUES (?, ?, ?, ?, ?)`), id, status, message, author, now.UnixMilli()); err != nil {
		return Incident{}, err
	}
	if err := tx.Commit(); err != nil {
		return Incident{}, err
	}
	return s.IncidentByID(ctx, id)
}

func resolvedAt(status string, now time.Time) int64 {
	if status == IncidentResolved {
		return now.UnixMilli()
	}
	return 0
}

// AddIncidentUpdate adds an update to an incident's timeline; its status
// becomes the incident's. An update that is not resolved reopens it.
func (s *Store) AddIncidentUpdate(ctx context.Context, id int64, status, message, author string, now time.Time) (Incident, error) {
	message, err := cleanUpdate(status, message)
	if err != nil {
		return Incident{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Incident{}, err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	res, err := tx.ExecContext(ctx, s.q(`UPDATE incidents SET status = ?, updated_at = ?, resolved_at = ? WHERE id = ?`),
		status, now.UnixMilli(), resolvedAt(status, now), id)
	if err != nil {
		return Incident{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Incident{}, ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO incident_updates (incident_id, status, message, author, created_at)
		VALUES (?, ?, ?, ?, ?)`), id, status, message, author, now.UnixMilli()); err != nil {
		return Incident{}, err
	}
	if err := tx.Commit(); err != nil {
		return Incident{}, err
	}
	return s.IncidentByID(ctx, id)
}

// RenameIncident changes an incident's title.
func (s *Store) RenameIncident(ctx context.Context, id int64, title string, now time.Time) (Incident, error) {
	title, err := cleanIncidentTitle(title)
	if err != nil {
		return Incident{}, err
	}
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE incidents SET title = ?, updated_at = ? WHERE id = ?`), title, now.UnixMilli(), id)
	if err != nil {
		return Incident{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Incident{}, ErrNotFound
	}
	return s.IncidentByID(ctx, id)
}

// DeleteIncident deletes an incident with its timeline.
func (s *Store) DeleteIncident(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, s.q(`DELETE FROM incidents WHERE id = ?`), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// IncidentByID returns the incident with this ID and its timeline, or
// ErrNotFound.
func (s *Store) IncidentByID(ctx context.Context, id int64) (Incident, error) {
	is, err := s.incidents(ctx, `WHERE id = ?`, id)
	if err != nil {
		return Incident{}, err
	}
	if len(is) == 0 {
		return Incident{}, ErrNotFound
	}
	return is[0], nil
}

// Incidents returns a page's incidents that are open or were resolved from
// since on, newest first, with their timelines.
func (s *Store) Incidents(ctx context.Context, pageID int64, since time.Time) ([]Incident, error) {
	return s.incidents(ctx, `WHERE page_id = ? AND (resolved_at = 0 OR resolved_at >= ?)`, pageID, since.UnixMilli())
}

func (s *Store) incidents(ctx context.Context, where string, args ...any) ([]Incident, error) {
	// where is one of the fixed clauses above; values are bound arguments.
	// nosemgrep
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT id, page_id, title, status, created_at, updated_at, resolved_at
		FROM incidents `+where+` ORDER BY created_at DESC, id DESC`), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Incident
	for rows.Next() {
		var in Incident
		var created, updated, resolved int64
		if err := rows.Scan(&in.ID, &in.PageID, &in.Title, &in.Status, &created, &updated, &resolved); err != nil {
			return nil, err
		}
		in.CreatedAt, in.UpdatedAt, in.ResolvedAt = fromMillis(created), fromMillis(updated), fromMillis(resolved)
		in.Updates = []IncidentUpdate{}
		out = append(out, in)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Updates, err = s.incidentUpdates(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) incidentUpdates(ctx context.Context, id int64) ([]IncidentUpdate, error) {
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT id, status, message, author, created_at FROM incident_updates
		WHERE incident_id = ? ORDER BY created_at DESC, id DESC`), id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IncidentUpdate{}
	for rows.Next() {
		var u IncidentUpdate
		var at int64
		if err := rows.Scan(&u.ID, &u.Status, &u.Message, &u.Author, &at); err != nil {
			return nil, err
		}
		u.CreatedAt = fromMillis(at)
		out = append(out, u)
	}
	return out, rows.Err()
}
