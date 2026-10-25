// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"time"

	"github.com/zjcochran/eyrie/internal/analysis"
	"github.com/zjcochran/eyrie/internal/backup"
	"github.com/zjcochran/eyrie/internal/notify"
	"github.com/zjcochran/eyrie/internal/store"
)

// mountinfo says what is mounted where, to warn when the backup folder is
// not a volume of its own.
var mountinfo = "/proc/self/mountinfo"

// makeBackup sends a backup file: {"history","passphrase"}.
func (s *Server) makeBackup(w http.ResponseWriter, r *http.Request) {
	var b struct {
		History    string `json:"history"`
		Passphrase string `json:"passphrase"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	if len(b.Passphrase) < backup.MinPassphrase {
		invalid(w, http.StatusUnprocessableEntity, fmt.Sprintf("The passphrase must be at least %d characters.", backup.MinPassphrase))
		return
	}
	if _, _, _, err := backup.HistoryRange(b.History, s.now()); err != nil {
		invalid(w, http.StatusUnprocessableEntity, "The history must be none, 30, 60, 90 or all.")
		return
	}
	// The file is made first, so an error is still an error answer.
	tmp, err := os.CreateTemp("", "eyrie-backup-*")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	meta, err := s.Backups.Write(r.Context(), tmp, b.History, b.Passphrase)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		s.fail(w, r, err)
		return
	}
	s.audit(r, "backup downloaded", "history", meta.History, "by", sessionFrom(r).User.Username)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+backup.Name(meta.Created)+`"`)
	w.Header().Set("Cache-Control", "no-store")
	io.Copy(w, tmp) //nolint:errcheck,gosec // the client may have gone
}

// restoreAnswer is what a restore answers: the backup's metadata.
type restoreAnswer struct {
	Restored backup.Meta `json:"restored"`
}

// restoreBackup replaces the data with a backup file's, sent as
// multipart/form-data: file and passphrase. Everyone is signed out.
func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) {
	s.restore(w, r, false)
}

// setupRestore restores a backup in place of setup: only while no
// account exists, without signing in.
func (s *Server) setupRestore(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if n > 0 {
		invalid(w, http.StatusForbidden, "Setup is already done: sign in.")
		return
	}
	s.restore(w, r, true)
}

func (s *Server) restore(w http.ResponseWriter, r *http.Request, setup bool) {
	f, pass, ok := readUpload(w, r)
	if !ok {
		return
	}
	defer f.Close()
	ctx := r.Context()
	meta, snap, err := s.Backups.Read(ctx, f, pass)
	if err != nil {
		invalid(w, http.StatusUnprocessableEntity, capitalize(err.Error())+".")
		return
	}
	err = s.Engine.Replace(ctx, func(ctx context.Context) error { return s.Store.Import(ctx, snap, setup) })
	switch {
	case errors.Is(err, store.ErrSetupDone):
		invalid(w, http.StatusForbidden, "Setup is already done: sign in.")
		return
	case errors.Is(err, store.ErrNoAdmin):
		invalid(w, http.StatusUnprocessableEntity, capitalize(err.Error())+".")
		return
	case err != nil:
		s.Log.Error("restore refused", "err", err)
		invalid(w, http.StatusUnprocessableEntity, "The backup cannot be restored: "+err.Error()+".")
		return
	}
	s.reloadSettings(r)
	by := "setup"
	if sess := sessionFrom(r); sess != nil {
		by = sess.User.Username
	}
	s.audit(r, "backup restored", "made", meta.Created, "version", meta.AppVersion, "history", meta.History, "by", by)
	s.hub.publish("monitors", map[string]int64{})
	// Every session ended with the users they belonged to.
	s.clearCookie(w, r, sessionCookie, "/")
	writeJSON(w, http.StatusOK, restoreAnswer{meta})
}

// readUpload reads a restore's form: the file (at most backup.MaxFile)
// and the passphrase. It answers the error itself.
func readUpload(w http.ResponseWriter, r *http.Request) (multipart.File, string, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, backup.MaxFile+1<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			invalid(w, http.StatusRequestEntityTooLarge, "The file is larger than 128 MB.")
		} else {
			invalid(w, http.StatusBadRequest, "Send the backup as multipart/form-data with file and passphrase.")
		}
		return nil, "", false
	}
	f, _, err := r.FormFile("file")
	if err != nil {
		invalid(w, http.StatusBadRequest, "Choose a backup file.")
		return nil, "", false
	}
	return f, r.FormValue("passphrase"), true
}

// reloadSettings makes restored settings take effect.
func (s *Server) reloadSettings(r *http.Request) {
	if a, err := analysis.LoadSettings(r.Context(), s.Store); err != nil {
		s.Log.Error("cannot read the restored alert analysis settings", "err", err)
	} else {
		s.Analyzer.SetSettings(a)
	}
	if n, err := notify.LoadSettings(r.Context(), s.Store); err != nil {
		s.Log.Error("cannot read the restored notification settings", "err", err)
	} else {
		s.Notifier.SetSettings(n)
	}
}

// scheduleView is the scheduled backups as admins see them: never the
// passphrase.
type scheduleView struct {
	Enabled       bool          `json:"enabled"`
	Time          string        `json:"time"`
	Keep          int           `json:"keep"`
	History       string        `json:"history"`
	HasPassphrase bool          `json:"has_passphrase"`
	Last          *lastView     `json:"last"` // null: never ran
	Files         []backup.File `json:"files"`
	Dir           string        `json:"dir"`
	NotMounted    bool          `json:"not_mounted"` // the folder is not a volume: files are lost with the container
	Error         string        `json:"error"`       // the folder cannot be read
}

type lastView struct {
	At      time.Time  `json:"at"`
	File    string     `json:"file"`
	Error   string     `json:"error"`
	Success *time.Time `json:"success"`
}

func (s *Server) viewSchedule(r *http.Request) (scheduleView, error) {
	b, last, err := s.Backups.Schedule(r.Context())
	if err != nil {
		return scheduleView{}, err
	}
	v := scheduleView{Enabled: b.Enabled, Time: b.Time, Keep: b.Keep, History: b.History, HasPassphrase: b.Passphrase != "",
		Dir: s.Backups.Dir, NotMounted: !s.Backups.Mounted(mountinfo)}
	if !last.At.IsZero() {
		v.Last = &lastView{At: last.At, File: last.File, Error: last.Error, Success: timeOrNil(last.Success)}
	}
	if v.Files, err = s.Backups.Files(); err != nil {
		v.Files, v.Error = []backup.File{}, "Cannot list "+s.Backups.Dir+": "+err.Error()
	}
	return v, nil
}

func (s *Server) getSchedule(w http.ResponseWriter, r *http.Request) {
	v, err := s.viewSchedule(r)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// saveSchedule saves the scheduled backups' settings: {"enabled","time",
// "keep","history","passphrase"}; an empty passphrase keeps the saved one.
func (s *Server) saveSchedule(w http.ResponseWriter, r *http.Request) {
	var b struct {
		backup.Schedule
		Passphrase string `json:"passphrase"`
	}
	if !readJSON(w, r, &b) {
		return
	}
	saved, err := s.Backups.SaveSchedule(r.Context(), b.Schedule, b.Passphrase)
	if err != nil {
		invalid(w, http.StatusUnprocessableEntity, capitalize(err.Error())+".")
		return
	}
	s.audit(r, "backup schedule saved", "enabled", saved.Enabled, "time", saved.Time, "keep", saved.Keep,
		"history", saved.History, "by", sessionFrom(r).User.Username)
	s.getSchedule(w, r)
}

// runBackup makes a scheduled backup now.
func (s *Server) runBackup(w http.ResponseWriter, r *http.Request) {
	b, _, err := s.Backups.Schedule(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if b.Passphrase == "" {
		invalid(w, http.StatusUnprocessableEntity, "Save a passphrase for the scheduled backups first.")
		return
	}
	s.Backups.RunNow(r.Context())
	s.getSchedule(w, r)
}

// storedBackup sends a file of the backup folder.
func (s *Server) storedBackup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path, ok := s.Backups.Path(name)
	if !ok {
		invalid(w, http.StatusNotFound, "No such backup.")
		return
	}
	f, err := os.Open(path) //nolint:gosec // G304: the name matched eyrie-backup-*.json and has no path in it
	if errors.Is(err, os.ErrNotExist) {
		invalid(w, http.StatusNotFound, "No such backup.")
		return
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	io.Copy(w, f) //nolint:errcheck,gosec // the client may have gone
}
