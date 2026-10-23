// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zjcochran/eyrie/internal/analysis"
	"github.com/zjcochran/eyrie/internal/secrets"
	"github.com/zjcochran/eyrie/internal/sso"
	"github.com/zjcochran/eyrie/internal/store"
)

const pass = "correct horse battery"

func sample() store.Snapshot {
	return store.Snapshot{Config: map[string][]store.Row{"settings": {{"key": "x", "value": "{}"}}}}
}

func TestEncodeDecode(t *testing.T) {
	meta := Meta{AppVersion: "v1.2.3", Schema: 6, Database: "sqlite", Created: time.Date(2027, 1, 15, 8, 0, 0, 0, time.UTC), History: "none"}
	var buf bytes.Buffer
	if err := Encode(&buf, meta, sample(), pass); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), `"settings"`) {
		t.Error("the data is readable")
	}
	got, snap, err := Decode(bytes.NewReader(buf.Bytes()), pass)
	if err != nil || got != meta || snap.Config["settings"][0]["key"] != "x" {
		t.Fatalf("%+v %+v %v", got, snap, err)
	}
	if _, _, err := Decode(bytes.NewReader(buf.Bytes()), "another passphrase"); !errors.Is(err, ErrDecrypt) {
		t.Errorf("wrong passphrase: %v", err)
	}
	if err := Encode(&buf, meta, sample(), "short"); err == nil {
		t.Error("short passphrase")
	}
	var f file
	json.Unmarshal(buf.Bytes(), &f)
	edit := func(change func(*file)) []byte {
		c := f
		c.KDF.Salt = bytes.Clone(f.KDF.Salt)
		change(&c)
		b, _ := json.Marshal(c)
		return b
	}
	for name, b := range map[string][]byte{
		"garbage":      []byte("nope"),
		"other format": edit(func(c *file) { c.Format = "other" }),
		"version":      edit(func(c *file) { c.Version = 2 }),
		"kdf":          edit(func(c *file) { c.KDF.Name = "scrypt" }),
		"memory":       edit(func(c *file) { c.KDF.Memory = 1 << 30 }),
		"time":         edit(func(c *file) { c.KDF.Time = 11 }),
		"threads":      edit(func(c *file) { c.KDF.Threads = 0 }),
		"salt":         edit(func(c *file) { c.KDF.Salt = c.KDF.Salt[:8] }),
		"nonce":        edit(func(c *file) { c.Nonce = c.Nonce[:4] }),
		"data":         edit(func(c *file) { c.Data = append(bytes.Clone(c.Data), 1) }),
	} {
		if _, _, err := Decode(bytes.NewReader(b), pass); err == nil {
			t.Errorf("%s decoded", name)
		}
	}
	// Encrypted but not a payload: not gzip, or no configuration.
	for _, plain := range [][]byte{[]byte("plain"), gz(t, `{"meta":{},"data":{}}`), gz(t, `[`)} {
		c := f
		aead, _ := newAEAD(pass, c.KDF)
		c.Data = aead.Seal(nil, c.Nonce, plain, []byte(FormatName))
		b, _ := json.Marshal(c)
		if _, _, err := Decode(bytes.NewReader(b), pass); err == nil {
			t.Errorf("%q decoded", plain)
		}
	}
}

func gz(t *testing.T, s string) []byte {
	t.Helper()
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	zw.Write([]byte(s))
	zw.Close()
	return b.Bytes()
}

func TestHistoryRange(t *testing.T) {
	now := time.Date(2027, 3, 31, 0, 0, 0, 0, time.UTC)
	for choice, want := range map[string]string{"": "none", "none": "none", "30": "30d", "60": "60d", "90": "90d", "all": "all"} {
		inc, since, label, err := HistoryRange(choice, now)
		if err != nil || label != want || inc != (want != "none") {
			t.Errorf("%q: %v %v %s %v", choice, inc, since, label, err)
		}
	}
	if _, since, _, _ := HistoryRange("30", now); !since.Equal(now.AddDate(0, 0, -30)) {
		t.Errorf("since %v", since)
	}
	if _, _, _, err := HistoryRange("7", now); !errors.Is(err, ErrHistoryRange) {
		t.Error("7 days")
	}
	if n := Name(time.Date(2027, 1, 2, 3, 4, 5, 0, time.FixedZone("X", 3600))); n != "eyrie-backup-20270102-020405.json" {
		t.Errorf("name %s", n)
	}
}

// TestSecretsMoveBetweenKeys seals secrets with one key, makes a backup and
// restores it with another.
func TestSecretsMoveBetweenKeys(t *testing.T) {
	a, _ := secrets.New("key a")
	b, _ := secrets.New("key b")
	seal := func(box *secrets.Box, s string) string { v, _ := box.Seal(s); return v }
	settingJSON := func(v any) string { j, _ := json.Marshal(v); return string(j) }
	snap := store.Snapshot{Config: map[string][]store.Row{
		"users":                 {{"totp_secret": seal(a, "TOTPSECRET")}, {"totp_secret": ""}},
		"notification_channels": {{"url": seal(a, "https://ntfy.example.com/x"), "token": seal(a, "tk"), "username": ""}},
		"settings": {
			{"key": sso.SettingKey, "value": settingJSON(map[string]any{"client_secret": seal(a, "client"), "client_id": "eyrie"})},
			{"key": analysis.SettingKey, "value": settingJSON(map[string]any{"api_key": seal(a, "sk-key"), "local_api_key": ""})},
			{"key": "retention", "value": `{"heartbeat_days":90}`},
		},
		"monitors": {
			{"name": "R", "type": "radius", "config": `{"host":"r.example.com","port":1812,"secret":"` + seal(a, "s3cret") + `","username":"alice","password":"","nas_identifier":"eyrie","called_station_id":"","calling_station_id":""}`},
			{"name": "P", "type": "port", "config": `{"host":"db.example.com","port":5432}`},
		},
	}}
	if err := Unseal(snap, a); err != nil {
		t.Fatal(err)
	}
	all, _ := json.Marshal(snap)
	for _, plain := range []string{"TOTPSECRET", "ntfy.example.com/x", "client", "sk-key", "s3cret"} {
		if !strings.Contains(string(all), plain) {
			t.Errorf("%s not unsealed", plain)
		}
	}
	if err := Reseal(snap, b); err != nil {
		t.Fatal(err)
	}
	open := func(v any) string {
		s, err := b.Open(v.(string))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if open(snap.Config["users"][0]["totp_secret"]) != "TOTPSECRET" || open(snap.Config["notification_channels"][0]["token"]) != "tk" {
		t.Error("columns")
	}
	var o map[string]string
	json.Unmarshal([]byte(snap.Config["settings"][0]["value"].(string)), &o)
	if open(o["client_secret"]) != "client" || o["client_id"] != "eyrie" {
		t.Errorf("sso %v", o)
	}
	var r map[string]any
	json.Unmarshal([]byte(snap.Config["monitors"][0]["config"].(string)), &r)
	if open(r["secret"]) != "s3cret" {
		t.Errorf("monitor %v", r)
	}
	// A value sealed with another key, or settings that are not JSON, fail.
	bads := []store.Snapshot{
		{Config: map[string][]store.Row{"users": {{"totp_secret": "v1:nope"}}}},
		{Config: map[string][]store.Row{"settings": {{"key": "sso", "value": "["}}}},
		{Config: map[string][]store.Row{"settings": {{"key": "sso", "value": `{"client_secret":"v1:nope"}`}}}},
		{Config: map[string][]store.Row{"monitors": {{"type": "radius", "config": `{"secret":"v1:nope"}`}}}},
		{Config: map[string][]store.Row{"monitors": {{"type": "telepathy", "config": `{}`}}}},
	}
	for _, bad := range bads {
		if err := Unseal(bad, a); err == nil {
			t.Errorf("%v unsealed", bad)
		}
	}
}

func newBackups(t *testing.T) (*Backups, *time.Time) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "eyrie.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.CreateFirstAdmin(context.Background(), "alice", "a long password here", time.Now()); err != nil {
		t.Fatal(err)
	}
	box, _ := secrets.New("key")
	now := time.Date(2027, 1, 15, 2, 0, 0, 0, time.Local)
	b := &Backups{Store: st, Secrets: box, Dir: filepath.Join(t.TempDir(), "backups"), Version: "v1.2.3",
		Log: slog.New(slog.DiscardHandler), Now: func() time.Time { return now }}
	return b, &now
}

func TestWriteRead(t *testing.T) {
	b, _ := newBackups(t)
	ctx := context.Background()
	var buf bytes.Buffer
	meta, err := b.Write(ctx, &buf, "all", pass)
	if err != nil || meta.History != "all" || meta.Schema < 6 || meta.Database != "sqlite" {
		t.Fatalf("%+v %v", meta, err)
	}
	if _, snap, err := b.Read(ctx, bytes.NewReader(buf.Bytes()), pass); err != nil || len(snap.Config["users"]) != 1 {
		t.Errorf("read: %v", err)
	}
	if _, err := b.Write(ctx, &buf, "all", "short"); err == nil {
		t.Error("short passphrase")
	}
	if _, err := b.Write(ctx, &buf, "7", pass); err == nil {
		t.Error("7 days")
	}
	// A backup of a newer schema is refused.
	var newer bytes.Buffer
	Encode(&newer, Meta{AppVersion: "v9.0.0", Schema: 999}, sample(), pass)
	if _, _, err := b.Read(ctx, &newer, pass); err == nil || !strings.Contains(err.Error(), "newer version of Eyrie (v9.0.0)") {
		t.Errorf("newer: %v", err)
	}
	if _, _, err := b.Read(ctx, strings.NewReader("{}"), pass); !errors.Is(err, ErrNotBackup) {
		t.Errorf("not a backup: %v", err)
	}
	// A stored secret this key cannot open.
	b.Store.SetSetting(ctx, "sso", map[string]string{"client_secret": "v1:nope"}, time.Now())
	if _, err := b.Write(ctx, &buf, "none", pass); err == nil || !strings.Contains(err.Error(), "cannot open a stored secret") {
		t.Errorf("unopenable: %v", err)
	}
}

func TestSchedule(t *testing.T) {
	b, now := newBackups(t)
	ctx := context.Background()
	s, last, err := b.Schedule(ctx)
	if err != nil || s != DefaultSchedule() || !last.At.IsZero() {
		t.Fatalf("defaults %+v %+v %v", s, last, err)
	}
	b.due(ctx) // off: nothing
	if files, _ := b.Files(); len(files) != 0 {
		t.Error("a backup while off")
	}
	if _, err := b.SaveSchedule(ctx, Schedule{Enabled: true, Time: "03:00", Keep: 2, History: "none"}, pass); err != nil {
		t.Fatal(err)
	}
	// 02:00 is before today's 03:00, but yesterday's was never made.
	b.due(ctx)
	files, _ := b.Files()
	_, last, _ = b.Schedule(ctx)
	if len(files) != 1 || last.File != files[0].Name || last.Success.IsZero() {
		t.Fatalf("first: %+v %+v", files, last)
	}
	b.due(ctx)
	if files, _ = b.Files(); len(files) != 1 {
		t.Error("made twice")
	}
	*now = now.Add(90 * time.Minute) // 03:30
	b.due(ctx)
	*now = now.Add(24 * time.Hour)
	b.due(ctx)
	if files, _ = b.Files(); len(files) != 2 {
		t.Errorf("kept %d", len(files))
	}
	// Other files in the folder are left alone.
	os.WriteFile(filepath.Join(b.Dir, "notes.txt"), nil, 0o600)
	*now = now.Add(24 * time.Hour)
	b.due(ctx)
	if _, err := os.Stat(filepath.Join(b.Dir, "notes.txt")); err != nil {
		t.Error("another file removed")
	}
	if p, ok := b.Path(files[0].Name); !ok || filepath.Dir(p) != b.Dir {
		t.Errorf("path %s", p)
	}
	if _, ok := b.Path("../eyrie.db"); ok {
		t.Error("path outside the folder")
	}
}

func TestScheduleFails(t *testing.T) {
	b, now := newBackups(t)
	ctx := context.Background()
	for _, s := range []Schedule{{Time: "25:00", Keep: 1, History: "none"}, {Time: "03:00", Keep: 400, History: "none"},
		{Time: "03:00", Keep: 1, History: "7"}, {Enabled: true, Time: "03:00", Keep: 1, History: "none"}} {
		if _, err := b.SaveSchedule(ctx, s, ""); err == nil {
			t.Errorf("%+v saved", s)
		}
	}
	if _, err := b.SaveSchedule(ctx, Schedule{Time: "03:00", Keep: 1, History: "none"}, "short"); err == nil {
		t.Error("short passphrase saved")
	}
	if last := b.RunNow(ctx); last.Error != "no passphrase is saved" {
		t.Errorf("no passphrase: %+v", last)
	}
	b.SaveSchedule(ctx, Schedule{Enabled: true, Time: "03:00", Keep: 1, History: "none"}, pass)
	// The folder is a file.
	os.WriteFile(filepath.Join(filepath.Dir(b.Dir), "file"), nil, 0o600)
	b.Dir = filepath.Join(filepath.Dir(b.Dir), "file")
	if last := b.RunNow(ctx); last.Error == "" || !last.Success.IsZero() {
		t.Errorf("failed: %+v", last)
	}
	if _, err := b.Files(); err == nil {
		t.Error("listed a file")
	}
	// A failure waits for the next day.
	_, last, _ := b.Schedule(ctx)
	b.due(ctx)
	if _, again, _ := b.Schedule(ctx); !again.At.Equal(last.At) {
		t.Error("retried at once")
	}
	*now = now.Add(48 * time.Hour)
	b.Store.Close()
	b.due(ctx) // cannot read: logged
	ctx2, cancel := context.WithCancel(ctx)
	cancel()
	b.Run(ctx2)
}

func TestMounted(t *testing.T) {
	info := filepath.Join(t.TempDir(), "mountinfo")
	os.WriteFile(info, []byte("22 1 0:21 / / rw - overlay overlay rw\n30 22 8:1 / /data rw - ext4 /dev/sda1 rw\nshort line\n"), 0o600)
	for dir, want := range map[string]bool{"/data/backups": true, "/data": true, "/backups": false, "/database": false} {
		if got := (&Backups{Dir: dir}).Mounted(info); got != want {
			t.Errorf("%s: %v", dir, got)
		}
	}
	if !(&Backups{Dir: "/x"}).Mounted(filepath.Join(t.TempDir(), "none")) {
		t.Error("unknown is mounted")
	}
}
