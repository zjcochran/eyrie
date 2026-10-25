// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/zjcochran/eyrie/internal/workload"
)

// AgentTokenPrefix starts every agent enrollment token.
const AgentTokenPrefix = "eya_"

// Agent is one of Eyrie's agents on another host.
type Agent struct {
	ID           int64
	Name         string
	Platform     string
	Hostname     string
	Version      string
	TokenExpires time.Time // zero: no enrollment token waits
	PublicKey    string    // "": not enrolled
	PreviousKey  string    // the key before a rotation, until the new one is used
	KeyCreated   time.Time
	LastSeen     time.Time
	LastAddress  string
	Error        string // why the last report has no workloads
	Workloads    []workload.Workload
	CreatedAt    time.Time
}

// AgentReport is what an agent's report changes.
type AgentReport struct {
	Seen      time.Time
	Address   string
	Version   string
	Platform  string
	Hostname  string
	Error     string
	Workloads []workload.Workload // nil: unchanged
}

const agentCols = `id, name, platform, hostname, version, token_expires, public_key, previous_key, key_created,
	last_seen, last_address, error, workloads, created_at`

func scanAgent(sc interface{ Scan(...any) error }) (Agent, error) {
	var a Agent
	var expires, keyCreated, seen, created int64
	var ws string
	err := sc.Scan(&a.ID, &a.Name, &a.Platform, &a.Hostname, &a.Version, &expires, &a.PublicKey, &a.PreviousKey,
		&keyCreated, &seen, &a.LastAddress, &a.Error, &ws, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, err
	}
	if err := json.Unmarshal([]byte(ws), &a.Workloads); err != nil {
		return Agent{}, err
	}
	a.TokenExpires, a.KeyCreated, a.LastSeen, a.CreatedAt = millisOrZeroTime(expires), millisOrZeroTime(keyCreated), millisOrZeroTime(seen), fromMillis(created)
	return a, nil
}

// millisOrZeroTime is fromMillis, with 0 as the zero time.
func millisOrZeroTime(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return fromMillis(ms)
}

// AddAgent adds an agent waiting to enroll, and returns its one-time
// enrollment token, valid until expires.
func (s *Store) AddAgent(ctx context.Context, name string, now, expires time.Time) (Agent, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 64 {
		return Agent{}, "", invalidf("the name must be 1 to 64 characters")
	}
	token := AgentTokenPrefix + randomToken()
	var id int64
	// Parameterized: every value is a bound argument.
	// nosemgrep
	err := s.db.QueryRowContext(ctx, s.q(`INSERT INTO agents (name, token_hash, token_expires, created_at) VALUES (?, ?, ?, ?) RETURNING id`),
		name, hashToken(token), expires.UnixMilli(), now.UnixMilli()).Scan(&id)
	if isUnique(err) {
		return Agent{}, "", invalidf("there is already an agent named %s", name)
	}
	if err != nil {
		return Agent{}, "", err
	}
	a, err := s.Agent(ctx, id)
	return a, token, err
}

// Agent returns one agent.
func (s *Store) Agent(ctx context.Context, id int64) (Agent, error) {
	return scanAgent(s.db.QueryRowContext(ctx, s.q(`SELECT `+agentCols+` FROM agents WHERE id = ?`), id))
}

// Agents lists the agents by name.
func (s *Store) Agents(ctx context.Context) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+agentCols+` FROM agents ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Agent{}
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// NewAgentToken gives an agent a new one-time enrollment token and forgets
// its key: the agent must enroll again.
func (s *Store) NewAgentToken(ctx context.Context, id int64, expires time.Time) (string, error) {
	token := AgentTokenPrefix + randomToken()
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE agents SET token_hash = ?, token_expires = ?, public_key = '', previous_key = '', key_created = 0 WHERE id = ?`),
		hashToken(token), expires.UnixMilli(), id)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", ErrNotFound
	}
	return token, nil
}

// EnrollAgent trades a valid enrollment token for the agent's key. It
// returns ErrNotFound for an unknown, used or expired token.
func (s *Store) EnrollAgent(ctx context.Context, token, publicKey string, r AgentReport) (Agent, error) {
	if !strings.HasPrefix(token, AgentTokenPrefix) {
		return Agent{}, ErrNotFound
	}
	var id int64
	err := s.db.QueryRowContext(ctx, s.q(`
		UPDATE agents SET token_hash = '', token_expires = 0, public_key = ?, previous_key = '', key_created = ?,
			last_seen = ?, last_address = ?, version = ?, platform = ?, hostname = ?, error = '', workloads = '[]'
		WHERE token_hash = ? AND token_expires > ? RETURNING id`),
		publicKey, r.Seen.UnixMilli(), r.Seen.UnixMilli(), r.Address, r.Version, r.Platform, r.Hostname,
		hashToken(token), r.Seen.UnixMilli()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, ErrNotFound
	}
	if err != nil {
		return Agent{}, err
	}
	return s.Agent(ctx, id)
}

// SaveAgentReport records a report.
func (s *Store) SaveAgentReport(ctx context.Context, id int64, r AgentReport) error {
	q := `UPDATE agents SET last_seen = ?, last_address = ?, version = ?, platform = ?, hostname = ?, error = ?`
	args := []any{r.Seen.UnixMilli(), r.Address, r.Version, r.Platform, r.Hostname, r.Error}
	if r.Workloads != nil {
		b, err := json.Marshal(r.Workloads)
		if err != nil {
			return err
		}
		q += `, workloads = ?`
		args = append(args, string(b))
	}
	// Only fixed SQL is added above; the values are bound arguments.
	// nosemgrep
	_, err := s.db.ExecContext(ctx, s.q(q+` WHERE id = ?`), append(args, id)...)
	return err
}

// RotateAgentKey makes newKey the agent's key, keeping the old one as its
// previous key until the agent signs with the new one.
func (s *Store) RotateAgentKey(ctx context.Context, id int64, newKey string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, s.q(`UPDATE agents SET previous_key = public_key, public_key = ?, key_created = ? WHERE id = ? AND public_key <> ''`),
		newKey, now.UnixMilli(), id)
	return err
}

// ClearAgentPreviousKey forgets an agent's previous key, once it has used
// the new one.
func (s *Store) ClearAgentPreviousKey(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, s.q(`UPDATE agents SET previous_key = '' WHERE id = ?`), id)
	return err
}

// DeleteAgent removes an agent, and returns its name.
func (s *Store) DeleteAgent(ctx context.Context, id int64) (string, error) {
	var name string
	err := s.db.QueryRowContext(ctx, s.q(`DELETE FROM agents WHERE id = ? RETURNING name`), id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return name, err
}
