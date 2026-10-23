// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
)

// Sealer seals secrets for the database and opens them again.
type Sealer interface {
	Seal(plain string) (string, error)
	Open(sealed string) (string, error)
}

// secretive settings have secrets (passwords, keys, tokens): they are
// sealed in the database, never shown, and kept when a change leaves them
// empty. normalize may fill in a default for one or check that it is set,
// but never reads it otherwise: it can be sealed.
type secretive interface {
	// secrets are the secret settings, by their JSON names.
	secrets() map[string]*string
}

// plainChecker settings check secrets that were just typed in, before
// they are sealed: plain says which secrets those are.
type plainChecker interface {
	checkPlain(plain func(name string) bool) error
}

func secretsOf(s settings) map[string]*string {
	if sv, ok := s.(secretive); ok {
		return sv.secrets()
	}
	return nil
}

// decode reads a type's settings from JSON, without normalizing them.
func decode(typ string, raw json.RawMessage) (Type, settings, error) {
	t, ok := Types[typ]
	if !ok {
		return t, nil, fmt.Errorf("unknown monitor type %q", typ)
	}
	s := t.settings()
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(s); err != nil {
		return t, nil, fmt.Errorf("the %s settings are not right: %w", t.Label, err)
	}
	return t, s, nil
}

// Prepare reads the settings an admin sent for a monitor, fills in
// defaults and checks them, and returns them as they are saved: with their
// secrets sealed. An empty secret keeps the one in saved (the monitor's
// saved settings, or nil), unless its name is in drop. Errors are meant
// for people.
func Prepare(env *Env, typ string, raw, saved json.RawMessage, drop []string) (json.RawMessage, error) {
	_, s, err := decode(typ, raw)
	if err != nil {
		return nil, err
	}
	kept := map[string]bool{}
	if old := savedSecrets(typ, saved); old != nil {
		for name, v := range secretsOf(s) {
			if *v == "" && !slices.Contains(drop, name) && old[name] != "" {
				*v, kept[name] = old[name], true
			}
		}
	}
	if err := s.normalize(); err != nil {
		return nil, err
	}
	if pc, ok := s.(plainChecker); ok {
		if err := pc.checkPlain(func(name string) bool { return !kept[name] }); err != nil {
			return nil, err
		}
	}
	for name, v := range secretsOf(s) {
		if kept[name] || *v == "" || env.Secrets == nil {
			continue
		}
		if *v, err = env.Secrets.Seal(*v); err != nil {
			return nil, fmt.Errorf("cannot seal the %s: %w", name, err)
		}
	}
	return json.Marshal(s)
}

func savedSecrets(typ string, saved json.RawMessage) map[string]string {
	if len(saved) == 0 {
		return nil
	}
	_, s, err := decode(typ, saved)
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for name, v := range secretsOf(s) {
		out[name] = *v
	}
	return out
}

// Public returns saved settings without their secrets, and the names of
// the secrets that are set, sorted.
func Public(typ string, saved json.RawMessage) (json.RawMessage, []string) {
	set := []string{}
	_, s, err := decode(typ, saved)
	if err != nil {
		return saved, set
	}
	sec := secretsOf(s)
	if len(sec) == 0 {
		return saved, set
	}
	for name, v := range sec {
		if *v != "" {
			set = append(set, name)
		}
		*v = ""
	}
	sort.Strings(set)
	out, err := json.Marshal(s)
	if err != nil {
		return json.RawMessage(`{}`), set
	}
	return out, set
}

// open opens the sealed secrets of saved settings in place.
func open(env *Env, s settings) error {
	if env.Secrets == nil {
		return nil
	}
	for name, v := range secretsOf(s) {
		plain, err := env.Secrets.Open(*v)
		if err != nil {
			return fmt.Errorf("cannot open the %s: %w", name, err)
		}
		*v = plain
	}
	return nil
}
