// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"encoding/json"
	"fmt"

	"github.com/zjcochran/eyrie/internal/check"
	"github.com/zjcochran/eyrie/internal/store"
)

// Sealer seals and opens stored secrets with an instance's key.
type Sealer interface {
	Seal(plain string) (string, error)
	Open(sealed string) (string, error)
}

// sealedColumns are the table columns that hold sealed values.
var sealedColumns = map[string][]string{
	"users":                 {"totp_secret"},
	"notification_channels": {"url", "token", "username"},
}

// sealedSettings are, by setting, the sealed fields of its JSON value.
var sealedSettings = map[string][]string{
	"sso":           {"client_secret"},
	"analysis":      {"api_key", "local_api_key"},
	SettingSchedule: {"passphrase"},
}

// Unseal opens every sealed value of snap with this instance's key, for a
// backup file: there they are protected by the file's passphrase.
func Unseal(snap store.Snapshot, box Sealer) error { return mapSecrets(snap, box.Open) }

// Reseal seals every secret of snap from a backup file with this
// instance's key, before it is restored.
func Reseal(snap store.Snapshot, box Sealer) error { return mapSecrets(snap, box.Seal) }

func mapSecrets(snap store.Snapshot, f func(string) (string, error)) error {
	for table, cols := range sealedColumns {
		for i, r := range snap.Config[table] {
			for _, c := range cols {
				v, ok := r[c].(string)
				if !ok {
					continue
				}
				var err error
				if r[c], err = f(v); err != nil {
					return fmt.Errorf("%s row %d, %s: %w", table, i+1, c, err)
				}
			}
		}
	}
	for i, r := range snap.Config["settings"] {
		key, _ := r["key"].(string)
		fields := sealedSettings[key]
		raw, ok := r["value"].(string)
		if len(fields) == 0 || !ok {
			continue
		}
		var v map[string]any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			return fmt.Errorf("setting %s: %w", key, err)
		}
		for _, field := range fields {
			s, ok := v[field].(string)
			if !ok {
				continue
			}
			var err error
			if v[field], err = f(s); err != nil {
				return fmt.Errorf("setting %s, %s: %w", key, field, err)
			}
		}
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		snap.Config["settings"][i]["value"] = string(b)
	}
	for i, r := range snap.Config["monitors"] {
		typ, _ := r["type"].(string)
		cfg, ok := r["config"].(string)
		if !ok {
			continue
		}
		out, err := check.MapSecrets(typ, json.RawMessage(cfg), f)
		if err != nil {
			return fmt.Errorf("monitor %v: %w", r["name"], err)
		}
		snap.Config["monitors"][i]["config"] = string(out)
	}
	return nil
}
