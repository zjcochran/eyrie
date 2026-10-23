// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Channel is a notification channel as stored: URL, Token and Username
// sealed by the caller (package notify knows the kinds).
type Channel struct {
	ID         int64
	Name       string
	Kind       string
	URL        string
	Token      string
	Username   string
	Recipients string
	Sender     string
	Enabled    bool
	Default    bool // given to new monitors
	CreatedAt  time.Time
	UpdatedAt  time.Time
	Monitors   int // monitors that use it
}

const channelCols = `id, name, kind, url, token, username, recipients, sender, enabled, is_default, created_at, updated_at,
	(SELECT COUNT(*) FROM monitor_channels mc WHERE mc.channel_id = notification_channels.id)`

func scanChannel(sc interface{ Scan(...any) error }) (Channel, error) {
	var c Channel
	var created, updated int64
	err := sc.Scan(&c.ID, &c.Name, &c.Kind, &c.URL, &c.Token, &c.Username, &c.Recipients, &c.Sender, &c.Enabled, &c.Default,
		&created, &updated, &c.Monitors)
	if errors.Is(err, sql.ErrNoRows) {
		return Channel{}, ErrNotFound
	}
	c.CreatedAt, c.UpdatedAt = fromMillis(created), fromMillis(updated)
	return c, err
}

func cleanChannelName(c *Channel) error {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" || len([]rune(c.Name)) > 100 {
		return invalidf("the channel's name must be 1 to 100 characters")
	}
	return nil
}

// CreateChannel saves a new channel. With toAll, every monitor starts
// using it.
func (s *Store) CreateChannel(ctx context.Context, c Channel, toAll bool, now time.Time) (Channel, error) {
	if err := cleanChannelName(&c); err != nil {
		return Channel{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Channel{}, err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	var id int64
	// Parameterized: every value is a bound argument, not SQL text.
	// nosemgrep
	err = tx.QueryRowContext(ctx, s.q(`
		INSERT INTO notification_channels (name, kind, url, token, username, recipients, sender, enabled, is_default,
			created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`),
		c.Name, c.Kind, c.URL, c.Token, c.Username, c.Recipients, c.Sender, c.Enabled, c.Default,
		now.UnixMilli(), now.UnixMilli()).Scan(&id)
	if err != nil {
		return Channel{}, err
	}
	if toAll {
		if err := s.channelToAll(ctx, tx, id); err != nil {
			return Channel{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Channel{}, err
	}
	return s.ChannelByID(ctx, id)
}

func (s *Store) channelToAll(ctx context.Context, tx *sql.Tx, id int64) error {
	_, err := tx.ExecContext(ctx, s.q(`
		INSERT INTO monitor_channels (monitor_id, channel_id) SELECT id, CAST(? AS BIGINT) FROM monitors WHERE TRUE
		ON CONFLICT (monitor_id, channel_id) DO NOTHING`), id)
	return err
}

// UpdateChannel saves a channel's settings. With toAll, every monitor
// starts using it.
func (s *Store) UpdateChannel(ctx context.Context, c Channel, toAll bool, now time.Time) (Channel, error) {
	if err := cleanChannelName(&c); err != nil {
		return Channel{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Channel{}, err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	// Parameterized: every value is a bound argument, not SQL text.
	// nosemgrep
	res, err := tx.ExecContext(ctx, s.q(`
		UPDATE notification_channels SET name = ?, kind = ?, url = ?, token = ?, username = ?, recipients = ?, sender = ?,
			enabled = ?, is_default = ?, updated_at = ? WHERE id = ?`),
		c.Name, c.Kind, c.URL, c.Token, c.Username, c.Recipients, c.Sender, c.Enabled, c.Default, now.UnixMilli(), c.ID)
	if err != nil {
		return Channel{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Channel{}, ErrNotFound
	}
	if toAll {
		if err := s.channelToAll(ctx, tx, c.ID); err != nil {
			return Channel{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Channel{}, err
	}
	return s.ChannelByID(ctx, c.ID)
}

// DeleteChannel deletes a channel; its monitors stop using it.
func (s *Store) DeleteChannel(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, s.q(`DELETE FROM notification_channels WHERE id = ?`), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ChannelByID returns a channel, or ErrNotFound.
func (s *Store) ChannelByID(ctx context.Context, id int64) (Channel, error) {
	return scanChannel(s.db.QueryRowContext(ctx, s.q(`SELECT `+channelCols+` FROM notification_channels WHERE id = ?`), id))
}

func (s *Store) channels(ctx context.Context, where string, args ...any) ([]Channel, error) {
	// where is one of the constants below; args are bound.
	// nosemgrep
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT `+channelCols+` FROM notification_channels `+where+` ORDER BY name, id`), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Channel
	for rows.Next() {
		c, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Channels returns every channel, by name.
func (s *Store) Channels(ctx context.Context) ([]Channel, error) { return s.channels(ctx, "") }

// ChannelsForMonitor returns the enabled channels a monitor uses.
func (s *Store) ChannelsForMonitor(ctx context.Context, monitorID int64) ([]Channel, error) {
	return s.channels(ctx, `WHERE enabled = TRUE AND id IN (SELECT channel_id FROM monitor_channels WHERE monitor_id = ?)`, monitorID)
}

// MonitorChannels returns the channel IDs of every monitor.
func (s *Store) MonitorChannels(ctx context.Context) (map[int64][]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT monitor_id, channel_id FROM monitor_channels ORDER BY monitor_id, channel_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]int64{}
	for rows.Next() {
		var m, c int64
		if err := rows.Scan(&m, &c); err != nil {
			return nil, err
		}
		out[m] = append(out[m], c)
	}
	return out, rows.Err()
}

// DefaultChannels returns the IDs of the channels new monitors get.
func (s *Store) DefaultChannels(ctx context.Context) ([]int64, error) {
	cs, err := s.channels(ctx, `WHERE is_default = TRUE`)
	var ids []int64
	for _, c := range cs {
		ids = append(ids, c.ID)
	}
	return ids, err
}

// SetMonitorChannels replaces the channels a monitor uses.
func (s *Store) SetMonitorChannels(ctx context.Context, monitorID int64, ids []int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // a no-op after Commit
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM monitor_channels WHERE monitor_id = ?`), monitorID); err != nil {
		return err
	}
	for _, id := range ids {
		var ok bool
		if err := tx.QueryRowContext(ctx, s.q(`SELECT EXISTS (SELECT 1 FROM notification_channels WHERE id = ?)`), id).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return invalidf("there is no notification channel %d", id)
		}
		if _, err := tx.ExecContext(ctx, s.q(`
			INSERT INTO monitor_channels (monitor_id, channel_id) VALUES (?, ?) ON CONFLICT (monitor_id, channel_id) DO NOTHING`),
			monitorID, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}
