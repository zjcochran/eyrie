// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

const passphrase = "correct horse battery"

// upload sends a backup file and its passphrase as a form.
func (c *client) upload(path string, file []byte, pass string) (int, string) {
	c.e.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if file != nil {
		fw, _ := mw.CreateFormFile("file", "backup.json")
		fw.Write(file)
	}
	mw.WriteField("passphrase", pass)
	mw.Close()
	resp, b := c.do("POST", path, body.Bytes(), "Content-Type", mw.FormDataContentType())
	var e errorBody
	json.Unmarshal(b, &e)
	return resp.StatusCode, e.Error
}

// download makes a backup as c.
func (c *client) download(history string) []byte {
	c.e.t.Helper()
	resp, b := c.do("POST", "/api/v1/backup", map[string]string{"history": history, "passphrase": passphrase})
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Disposition"), `attachment; filename="eyrie-backup-`) {
		c.e.t.Fatalf("backup: %d %s", resp.StatusCode, b)
	}
	return b
}

// backedUp is an Eyrie with a monitor with a secret and a channel, and a
// backup of it.
func backedUp(t *testing.T) (*env, *client, []byte) {
	t.Helper()
	e := newEnv(t)
	admin := e.setup()
	create(t, admin, map[string]any{"name": "RADIUS", "type": "radius",
		"settings": map[string]any{"host": "radius.example.com", "username": "alice", "secret": "s3cret"}})
	if code := admin.call("POST", "/api/v1/channels", map[string]any{"name": "ntfy", "kind": "ntfy",
		"url": "https://ntfy.example.com/eyrie", "token": "tk_123", "enabled": true}, nil); code != http.StatusCreated {
		t.Fatalf("channel: %d", code)
	}
	for _, c := range []struct{ body map[string]string }{
		{map[string]string{"history": "all", "passphrase": "short"}},
		{map[string]string{"history": "7", "passphrase": passphrase}},
	} {
		if code, _ := admin.errorOf("POST", "/api/v1/backup", c.body); code != http.StatusUnprocessableEntity {
			t.Errorf("%v: %d", c.body, code)
		}
	}
	file := admin.download("all")
	if bytes.Contains(file, []byte("s3cret")) || bytes.Contains(file, []byte("tk_123")) {
		t.Error("the backup shows a secret")
	}
	return e, admin, file
}

func TestBackupRestore(t *testing.T) {
	e, admin, file := backedUp(t)

	// Changes after the backup are undone by the restore, and everyone is
	// signed out.
	create(t, admin, portMonitor("Later"))
	if code, msg := admin.upload("/api/v1/restore", file, passphrase); code != http.StatusOK {
		t.Fatalf("restore: %d %s", code, msg)
	}
	if code := admin.call("GET", "/api/v1/monitors", nil, nil); code != http.StatusUnauthorized {
		t.Errorf("still signed in: %d", code)
	}
	again := e.signIn("alice", pw)
	var mons []monitorView
	again.call("GET", "/api/v1/monitors", nil, &mons)
	if len(mons) != 1 || mons[0].Name != "RADIUS" || strings.Join(mons[0].SecretsSet, ",") != "secret" {
		t.Errorf("monitors after the restore: %+v", mons)
	}
	if st := e.s.Engine.Statuses(); len(st) != 1 {
		t.Errorf("engine: %+v", st)
	}
	var chans []channelView
	again.call("GET", "/api/v1/channels", nil, &chans)
	if len(chans) != 1 || !chans[0].HasToken || chans[0].URLHost != "https://ntfy.example.com" {
		t.Errorf("channels: %+v", chans)
	}
	if !strings.Contains(e.log.String(), "backup restored") {
		t.Error("not logged")
	}
	viewer, _ := e.addUser(again, "bob", "viewer")
	if code, _ := viewer.errorOf("POST", "/api/v1/backup", map[string]string{"history": "none", "passphrase": passphrase}); code != http.StatusForbidden {
		t.Errorf("viewer: %d", code)
	}
}

func TestBackupRefused(t *testing.T) {
	_, admin, file := backedUp(t)
	if code, msg := admin.upload("/api/v1/restore", file, "wrong passphrase"); code != http.StatusUnprocessableEntity ||
		msg != "Wrong passphrase, or the file is damaged." {
		t.Errorf("wrong passphrase: %d %s", code, msg)
	}
	if code, msg := admin.upload("/api/v1/restore", []byte(`{"hello":1}`), passphrase); code != http.StatusUnprocessableEntity ||
		msg != "This is not an Eyrie backup file." {
		t.Errorf("not a backup: %d %s", code, msg)
	}
	if code, _ := admin.upload("/api/v1/restore", nil, passphrase); code != http.StatusBadRequest {
		t.Errorf("no file: %d", code)
	}
	if code, _ := admin.errorOf("POST", "/api/v1/restore", map[string]string{}); code != http.StatusBadRequest {
		t.Errorf("JSON: %d", code)
	}
}

func TestSetupRestore(t *testing.T) {
	_, _, file := backedUp(t)
	// A fresh Eyrie restores it in place of setup, once.
	fresh := newEnv(t)
	c := fresh.client()
	if code, msg := c.upload("/api/v1/setup/restore", file, passphrase); code != http.StatusOK {
		t.Fatalf("setup restore: %d %s", code, msg)
	}
	fresh.signIn("alice", pw)
	if code, _ := c.upload("/api/v1/setup/restore", file, passphrase); code != http.StatusForbidden {
		t.Errorf("second setup restore: %d", code)
	}
}

func TestBackupSchedule(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	var v scheduleView
	if code := admin.call("GET", "/api/v1/settings/backup", nil, &v); code != http.StatusOK || v.Enabled || v.Time != "03:00" ||
		v.Keep != 14 || v.Last != nil || len(v.Files) != 0 || v.HasPassphrase {
		t.Errorf("defaults: %d %+v", code, v)
	}
	for _, body := range []map[string]any{
		{"enabled": true, "time": "03:00", "keep": 14, "history": "none"},
		{"time": "3am", "keep": 14, "history": "none"},
		{"time": "03:00", "keep": 0, "history": "none"},
		{"time": "03:00", "keep": 14, "history": "7"},
		{"time": "03:00", "keep": 14, "history": "none", "passphrase": "short"},
	} {
		if code, _ := admin.errorOf("PUT", "/api/v1/settings/backup", body); code != http.StatusUnprocessableEntity {
			t.Errorf("%v: %d", body, code)
		}
	}
	if code, _ := admin.errorOf("POST", "/api/v1/backups/run", nil); code != http.StatusUnprocessableEntity {
		t.Errorf("run without a passphrase: %d", code)
	}
}

func TestBackupScheduleRun(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	var v scheduleView
	body := map[string]any{"enabled": true, "time": "04:30", "keep": 2, "history": "30", "passphrase": passphrase}
	if code := admin.call("PUT", "/api/v1/settings/backup", body, &v); code != http.StatusOK || !v.Enabled || !v.HasPassphrase || v.Keep != 2 {
		t.Fatalf("save: %d %+v", code, v)
	}
	if code := admin.call("POST", "/api/v1/backups/run", nil, &v); code != http.StatusOK || v.Last == nil || v.Last.Error != "" ||
		len(v.Files) != 1 || v.Last.Success == nil {
		t.Fatalf("run: %d %+v", code, v)
	}
	resp, b := admin.do("GET", "/api/v1/backups/"+v.Files[0].Name, nil)
	if resp.StatusCode != http.StatusOK || !bytes.Contains(b, []byte(`"format":"eyrie-backup"`)) {
		t.Errorf("download: %d %.80s", resp.StatusCode, b)
	}
	for _, name := range []string{"..%2Fsecret.key", "eyrie-backup-20990101-000000.json"} {
		if resp, _ := admin.do("GET", "/api/v1/backups/"+name, nil); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: %d", name, resp.StatusCode)
		}
	}
	// Keep 2: the oldest goes.
	for range 2 {
		e.advance(1e9)
		admin.call("POST", "/api/v1/backups/run", nil, &v)
	}
	if len(v.Files) != 2 {
		t.Errorf("kept %d", len(v.Files))
	}
	resp, b = e.client().do("GET", "/metrics", nil, "Authorization", "Bearer m3trics")
	if resp.StatusCode != http.StatusOK || !bytes.Contains(b, []byte("eyrie_backup_last_success_timestamp_seconds ")) {
		t.Errorf("metrics: %s", b)
	}
}

func TestBackupScheduleFolder(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	var v scheduleView
	body := map[string]any{"enabled": true, "time": "04:30", "keep": 2, "history": "30", "passphrase": passphrase}
	admin.call("PUT", "/api/v1/settings/backup", body, nil)
	admin.call("POST", "/api/v1/backups/run", nil, &v)
	// An empty passphrase keeps the saved one.
	delete(body, "passphrase")
	if code := admin.call("PUT", "/api/v1/settings/backup", body, &v); code != http.StatusOK || !v.HasPassphrase {
		t.Errorf("kept passphrase: %d %+v", code, v)
	}
	// A folder that cannot be read: a file.
	e.s.Backups.Dir = filepath.Join(e.s.Backups.Dir, v.Files[0].Name)
	if admin.call("GET", "/api/v1/settings/backup", nil, &v); v.Error == "" {
		t.Errorf("unreadable folder: %+v", v)
	}
	if code := admin.call("POST", "/api/v1/backups/run", nil, &v); code != http.StatusOK || v.Last.Error == "" {
		t.Errorf("failed run: %d %+v", code, v.Last)
	}
}
