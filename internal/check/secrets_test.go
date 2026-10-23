// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
)

type badSealer struct{ sealer }

func (badSealer) Seal(string) (string, error) { return "", errors.New("no key") }

func TestSecrets(t *testing.T) {
	env := testEnv()
	env.Secrets = sealer{}
	in := `{"host":"radius.example.com","secret":"s3cret","username":"alice","password":"pw"}`
	saved, err := Prepare(env, "radius", json.RawMessage(in), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var r Radius
	json.Unmarshal(saved, &r)
	if r.Secret != "sealed:s3cret" || r.Password != "sealed:pw" || r.Port != 1812 {
		t.Errorf("saved %s", saved)
	}

	// Shown without secrets.
	pub, set := Public("radius", saved)
	if strings.Contains(string(pub), "sealed") || !slices.Equal(set, []string{"password", "secret"}) {
		t.Errorf("public %s %v", pub, set)
	}

	// A change without secrets keeps them; drop removes one; a new one is
	// sealed.
	again, err := Prepare(env, "radius", pub, saved, nil)
	if err != nil || string(again) != string(saved) {
		t.Errorf("kept: %s %v", again, err)
	}
	changed, err := Prepare(env, "radius", json.RawMessage(`{"host":"radius.example.com","username":"bob","password":"new"}`), saved, []string{"secret"})
	if err == nil {
		t.Errorf("the secret was dropped, but it is required: %s", changed)
	}
	changed, err = Prepare(env, "radius", json.RawMessage(`{"host":"radius.example.com","username":"bob","password":"new"}`), saved, nil)
	json.Unmarshal(changed, &r)
	if err != nil || r.Password != "sealed:new" || r.Secret != "sealed:s3cret" {
		t.Errorf("changed %s %v", changed, err)
	}

	// The checker gets the secrets opened.
	_, c, err := Parse(env, "radius", saved)
	if err != nil || c.(*radiusChecker).r.Secret != "s3cret" {
		t.Errorf("opened: %v", err)
	}
	if _, _, err := Parse(env, "radius", json.RawMessage(`{"host":"radius.example.com","secret":"plain","username":"alice"}`)); err == nil {
		t.Error("an unsealed secret opened")
	}
}

func TestSecretsEdges(t *testing.T) {
	env := testEnv()
	env.Secrets = sealer{}
	in := `{"host":"radius.example.com","secret":"s3cret","username":"alice","password":"pw"}`
	// Types without secrets, and broken settings.
	if pub, set := Public("http", json.RawMessage(`{"url":"https://example.com"}`)); string(pub) != `{"url":"https://example.com"}` || len(set) != 0 {
		t.Errorf("http %s %v", pub, set)
	}
	if pub, set := Public("radius", json.RawMessage(`{"nope":1}`)); string(pub) != `{"nope":1}` || len(set) != 0 {
		t.Errorf("broken %s %v", pub, set)
	}
	if _, err := Prepare(env, "radius", json.RawMessage(`{"host":"radius.example.com","username":"bob","secret":"x"}`), json.RawMessage(`{"nope":1}`), nil); err != nil {
		t.Errorf("broken saved settings: %v", err)
	}
	if _, err := Prepare(env, "telepathy", nil, nil, nil); err == nil {
		t.Error("unknown type")
	}
	if _, err := Prepare(env, "radius", json.RawMessage(`{"host":""}`), nil, nil); err == nil {
		t.Error("invalid settings")
	}
	env.Secrets = badSealer{}
	if _, err := Prepare(env, "radius", json.RawMessage(in), nil, nil); err == nil {
		t.Error("sealing failed silently")
	}
	env.Secrets = nil
	if out, err := Prepare(env, "radius", json.RawMessage(in), nil, nil); err != nil || !strings.Contains(string(out), `"s3cret"`) {
		t.Errorf("without a sealer: %s %v", out, err)
	}
}

func TestSentence(t *testing.T) {
	if sentence("") != "" || sentence("état") != "État" || failErr(errors.New("no")).Message != "No" {
		t.Error("sentence")
	}
}

func TestMapSecrets(t *testing.T) {
	up := func(s string) (string, error) { return strings.ToUpper(s), nil }
	out, err := MapSecrets("radius", json.RawMessage(`{"host":"r.example.com","secret":"abc","username":"alice"}`), up)
	if err != nil || !strings.Contains(string(out), `"secret":"ABC"`) || !strings.Contains(string(out), `"username":"alice"`) {
		t.Errorf("%s %v", out, err)
	}
	if out, err := MapSecrets("port", json.RawMessage(`{"host":"db.example.com","port":5432}`), up); err != nil || string(out) != `{"host":"db.example.com","port":5432}` {
		t.Errorf("no secrets: %s %v", out, err)
	}
	if _, err := MapSecrets("radius", json.RawMessage(`{"secret":"x"}`), func(string) (string, error) { return "", errors.New("no key") }); err == nil {
		t.Error("error lost")
	}
	if _, err := MapSecrets("telepathy", nil, up); err == nil {
		t.Error("unknown type")
	}
}
