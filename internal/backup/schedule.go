// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zjcochran/eyrie/internal/store"
)

// Settings of scheduled backups.
const (
	SettingSchedule = "backup_schedule"      // Schedule as JSON
	settingLast     = "backup_schedule_last" // Last as JSON
)

// Schedule makes an encrypted backup every day at Time (the server's local
// time) into the backup folder, keeping the newest Keep files.
type Schedule struct {
	Enabled    bool   `json:"enabled"`
	Time       string `json:"time"`       // "HH:MM"
	Keep       int    `json:"keep"`       // files to keep, 1 to 365
	History    string `json:"history"`    // a HistoryRange choice
	Passphrase string `json:"passphrase"` // sealed with the secret key
}

// DefaultSchedule is the schedule before one is saved: off.
func DefaultSchedule() Schedule { return Schedule{Time: "03:00", Keep: 14, History: "none"} }

// Last is the outcome of the latest scheduled backup.
type Last struct {
	At      time.Time `json:"at"`      // when it ran; zero: never
	File    string    `json:"file"`    // the file written; "" on failure
	Error   string    `json:"error"`   // why it failed
	Success time.Time `json:"success"` // the latest run that worked
}

// File is a backup file in the backup folder.
type File struct {
	Name string    `json:"name"`
	Size int64     `json:"size"`
	Time time.Time `json:"time"`
}

var scheduleTime = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// fileName matches the files Eyrie writes and prunes; nothing else in the
// folder is touched.
var fileName = regexp.MustCompile(`^eyrie-backup-[0-9]{8}-[0-9]{6}\.json$`)

// Backups makes backup files, now or on a schedule, and reads them back.
type Backups struct {
	Store   *store.Store
	Secrets Sealer
	Dir     string // the backup folder
	Version string // Eyrie's
	Log     *slog.Logger
	Now     func() time.Time // nil: time.Now

	mu sync.Mutex // one backup at a time
}

func (b *Backups) now() time.Time {
	if b.Now != nil {
		return b.Now()
	}
	return time.Now()
}

// Write makes a backup of the data, with history as HistoryRange takes it,
// encrypted with passphrase.
func (b *Backups) Write(ctx context.Context, w io.Writer, history, passphrase string) (Meta, error) {
	if len(passphrase) < MinPassphrase {
		return Meta{}, fmt.Errorf("the passphrase must be at least %d characters", MinPassphrase)
	}
	now := b.now()
	include, since, label, err := HistoryRange(history, now)
	if err != nil {
		return Meta{}, err
	}
	schema, err := b.Store.SchemaVersion(ctx)
	if err != nil {
		return Meta{}, err
	}
	snap, err := b.Store.Export(ctx, include, since)
	if err != nil {
		return Meta{}, err
	}
	if err := Unseal(snap, b.Secrets); err != nil {
		return Meta{}, fmt.Errorf("cannot open a stored secret: %w", err)
	}
	meta := Meta{AppVersion: b.Version, Schema: schema, Database: string(b.Store.Dialect()), Created: now.UTC(), History: label}
	return meta, Encode(w, meta, snap, passphrase)
}

// Read decodes a backup file for a restore, with its secrets sealed with
// this instance's key. It refuses a backup of a newer database schema.
func (b *Backups) Read(ctx context.Context, r io.Reader, passphrase string) (Meta, store.Snapshot, error) {
	meta, snap, err := Decode(r, passphrase)
	if err != nil {
		return meta, snap, err
	}
	schema, err := b.Store.SchemaVersion(ctx)
	if err != nil {
		return meta, snap, err
	}
	if meta.Schema > schema {
		return meta, snap, fmt.Errorf("this backup was made by a newer version of Eyrie (%s): upgrade Eyrie first", meta.AppVersion)
	}
	if err := Reseal(snap, b.Secrets); err != nil {
		return meta, snap, err
	}
	return meta, snap, nil
}

// Schedule reads the saved schedule and the last scheduled backup.
func (b *Backups) Schedule(ctx context.Context) (Schedule, Last, error) {
	s, last := DefaultSchedule(), Last{}
	if _, err := b.Store.GetSetting(ctx, SettingSchedule, &s); err != nil {
		return s, last, err
	}
	_, err := b.Store.GetSetting(ctx, settingLast, &last)
	return s, last, err
}

// SaveSchedule checks and saves a schedule. A passphrase is sealed; ""
// keeps the saved one. Errors are meant for people.
func (b *Backups) SaveSchedule(ctx context.Context, s Schedule, passphrase string) (Schedule, error) {
	old, _, err := b.Schedule(ctx)
	if err != nil {
		return s, err
	}
	switch {
	case !scheduleTime.MatchString(s.Time):
		return s, errors.New("the time must be HH:MM, such as 03:00")
	case s.Keep < 1 || s.Keep > 365:
		return s, errors.New("keep 1 to 365 backups")
	}
	if _, _, _, err := HistoryRange(s.History, b.now()); err != nil {
		return s, errors.New("the history must be none, 30, 60, 90 or all")
	}
	s.Passphrase = old.Passphrase
	if passphrase != "" {
		if len(passphrase) < MinPassphrase {
			return s, fmt.Errorf("the passphrase must be at least %d characters", MinPassphrase)
		}
		if s.Passphrase, err = b.Secrets.Seal(passphrase); err != nil {
			return s, err
		}
	}
	if s.Enabled && s.Passphrase == "" {
		return s, errors.New("set a passphrase for the scheduled backups")
	}
	return s, b.Store.SetSetting(ctx, SettingSchedule, s, b.now())
}

// slot is the latest scheduled time at or before now.
func (s Schedule) slot(now time.Time) time.Time {
	h, _ := strconv.Atoi(s.Time[:2])
	m, _ := strconv.Atoi(s.Time[3:])
	t := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if t.After(now) {
		t = t.AddDate(0, 0, -1)
	}
	return t
}

// Run makes scheduled backups until ctx ends. It looks every minute, and
// at once, so a backup missed while Eyrie was off is made when it starts.
func (b *Backups) Run(ctx context.Context) {
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		b.due(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (b *Backups) due(ctx context.Context) {
	s, last, err := b.Schedule(ctx)
	if err != nil {
		b.Log.Error("cannot read the backup schedule", "err", err)
		return
	}
	if s.Enabled && last.At.Before(s.slot(b.now().Local())) {
		b.RunNow(ctx)
	}
}

// RunNow makes one backup with the saved schedule's settings, records how
// it went and prunes old files. A failure is not retried before the next
// scheduled time, so a broken setup does not repeat every minute.
func (b *Backups) RunNow(ctx context.Context) Last {
	b.mu.Lock()
	defer b.mu.Unlock()
	s, last, err := b.Schedule(ctx)
	last.At, last.File, last.Error = b.now(), "", ""
	if err == nil {
		last.File, err = b.writeFile(ctx, s)
	}
	if err != nil {
		last.Error = err.Error()
		b.Log.Error("scheduled backup failed", "err", err)
	} else {
		last.Success = last.At
		b.Log.Info("scheduled backup written", "file", last.File)
	}
	if err := b.Store.SetSetting(ctx, settingLast, last, b.now()); err != nil {
		b.Log.Error("cannot record the scheduled backup", "err", err)
	}
	return last
}

func (b *Backups) writeFile(ctx context.Context, s Schedule) (string, error) {
	pass, err := b.Secrets.Open(s.Passphrase)
	if err != nil {
		return "", err
	}
	if pass == "" {
		return "", errors.New("no passphrase is saved")
	}
	if err := os.MkdirAll(b.Dir, 0o750); err != nil {
		return "", err
	}
	// Written under a temporary name and renamed, so a half-written file
	// never looks like a backup.
	tmp, err := os.CreateTemp(b.Dir, ".partial-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	meta, err := b.Write(ctx, tmp, s.History, pass)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", err
	}
	name := Name(meta.Created)
	if err := os.Rename(tmp.Name(), filepath.Join(b.Dir, name)); err != nil {
		return "", err
	}
	if err := b.prune(s.Keep); err != nil {
		b.Log.Warn("cannot remove old backups", "err", err)
	}
	return name, nil
}

// Files lists the backup files in the folder, newest first.
func (b *Backups) Files() ([]File, error) {
	entries, err := os.ReadDir(b.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return []File{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []File{}
	for _, e := range entries {
		if !e.Type().IsRegular() || !fileName.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, File{Name: e.Name(), Size: info.Size(), Time: info.ModTime().UTC()})
	}
	// The name holds the UTC time, so name order is time order.
	slices.SortFunc(out, func(x, y File) int { return strings.Compare(y.Name, x.Name) })
	return out, nil
}

// Path is where a backup file of the folder is, for a name Files gave;
// false for any other name.
func (b *Backups) Path(name string) (string, bool) {
	if !fileName.MatchString(name) {
		return "", false
	}
	return filepath.Join(b.Dir, name), true
}

func (b *Backups) prune(keep int) error {
	files, err := b.Files()
	if err != nil || len(files) <= keep {
		return err
	}
	var errs []error
	for _, f := range files[keep:] {
		errs = append(errs, os.Remove(filepath.Join(b.Dir, f.Name)))
	}
	return errors.Join(errs...)
}

// Mounted reports whether the backup folder is on a mount of its own (a
// volume), from mountinfo; true when that cannot be read (not Linux).
func (b *Backups) Mounted(mountinfo string) bool {
	f, err := os.Open(mountinfo) //nolint:gosec // G304: /proc/self/mountinfo, or a test's file
	if err != nil {
		return true
	}
	defer f.Close()
	dir := filepath.Clean(b.Dir)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 5 || fields[4] == "/" {
			continue
		}
		mp := fields[4]
		if dir == mp || strings.HasPrefix(dir, mp+"/") {
			return true
		}
	}
	return false
}
