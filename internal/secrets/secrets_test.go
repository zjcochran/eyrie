// SPDX-License-Identifier: AGPL-3.0-or-later

package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSealOpen(t *testing.T) {
	b, err := New("a key")
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := b.Seal("hunter2")
	if err != nil || !strings.HasPrefix(sealed, "v1:") || strings.Contains(sealed, "hunter2") {
		t.Fatalf("Seal = %q, %v", sealed, err)
	}
	if again, _ := b.Seal("hunter2"); again == sealed {
		t.Error("two seals of the same value are equal: the nonce is not random")
	}
	if got, err := b.Open(sealed); err != nil || got != "hunter2" {
		t.Errorf("Open = %q, %v", got, err)
	}
	if s, _ := b.Seal(""); s != "" {
		t.Errorf("Seal(\"\") = %q", s)
	}
	if s, err := b.Open(""); s != "" || err != nil {
		t.Errorf("Open(\"\") = %q, %v", s, err)
	}
}

func TestOpenErrors(t *testing.T) {
	b, _ := New("a key")
	other, _ := New("another key")
	sealed, _ := other.Seal("x")
	for _, s := range []string{"plain", "v1:!!!", "v1:AAAA", sealed} {
		if _, err := b.Open(s); err == nil {
			t.Errorf("Open(%q) worked", s)
		}
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	b, err := Load("", path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v %v", info, err)
	}
	sealed, _ := b.Seal("x")
	again, err := Load("", path) // reads the same key back
	if err != nil {
		t.Fatal(err)
	}
	if got, err := again.Open(sealed); err != nil || got != "x" {
		t.Errorf("reloaded Open = %q, %v", got, err)
	}

	env, err := Load("from the environment", filepath.Join(t.TempDir(), "unused"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.Open(sealed); err == nil {
		t.Error("the environment key opened a value sealed with the file key")
	}
}

func TestLoadErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load("", filepath.Join(dir, "missing", "secret.key")); err == nil {
		t.Error("a key file in a missing folder was written")
	}
	if _, err := Load("", dir); err == nil {
		t.Error("a folder was read as the key file")
	}
	empty := filepath.Join(dir, "empty.key")
	os.WriteFile(empty, []byte("\n"), 0o600)
	if _, err := Load("", empty); err == nil {
		t.Error("an empty key file was accepted")
	}
}
