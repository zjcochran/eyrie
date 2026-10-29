// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
)

// agentMonitor is what a monitor of the agent type of earlier versions
// becomes: the type that checks its workload, on its agent.
func agentMonitor(config string) (typ string, settings any, agent int64) {
	var a struct {
		Agent    int64  `json:"agent"`
		Workload string `json:"workload"`
	}
	if json.Unmarshal([]byte(config), &a) != nil {
		return "docker", map[string]string{"container": ""}, 0
	}
	parts := strings.Split(a.Workload, "/")
	switch {
	case len(parts) == 2 && parts[0] == "service":
		return "docker-swarm", map[string]string{"service": parts[1]}, a.Agent
	case len(parts) == 3:
		return "kubernetes", map[string]string{"namespace": parts[0], "kind": parts[1], "name": parts[2]}, a.Agent
	case len(parts) == 2 && (parts[0] == "lxc" || parts[0] == "qemu"):
		if id, err := strconv.Atoi(parts[1]); err == nil {
			return "proxmox", map[string]int{"vmid": id}, a.Agent
		}
	}
	return "docker", map[string]string{"container": a.Workload}, a.Agent
}

// convertAgentMonitors turns the agent monitors of earlier versions (type
// 'agent': an agent and the key of a workload it reported) into monitors
// of the workload's own type, checked on that agent. It runs when the
// store opens and after a restore, and does nothing once there are none.
func (s *Store) convertAgentMonitors(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, config FROM monitors WHERE type = 'agent'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type change struct {
		id, agent int64
		typ       string
		config    []byte
	}
	var changes []change
	for rows.Next() {
		var id int64
		var config string
		if err := rows.Scan(&id, &config); err != nil {
			return err
		}
		typ, settings, agent := agentMonitor(config)
		raw, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		changes = append(changes, change{id, agent, typ, raw})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, c := range changes {
		if _, err := tx.ExecContext(ctx, s.q(`UPDATE monitors SET type = ?, config = ?, agent_id = ? WHERE id = ?`),
			c.typ, string(c.config), nullable(c.agent), c.id); err != nil {
			return err
		}
	}
	return nil
}
