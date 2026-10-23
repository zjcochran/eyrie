// SPDX-License-Identifier: AGPL-3.0-or-later

// Package backup reads and writes backup files: a store.Snapshot and its
// metadata, gzip-compressed and encrypted with a passphrase (argon2id key,
// AES-256-GCM). The file is one JSON object; only the metadata in it is
// readable without the passphrase, and the encrypted copy of the metadata is
// the one used. Secrets travel unsealed inside the encryption, so a backup
// restores on an instance with another secret key.
package backup

import (
	"bytes"
	"compress/gzip"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/zjcochran/eyrie/internal/store"
)

// FormatName marks an Eyrie backup file.
const FormatName = "eyrie-backup"

// Version is the file format written; Decode reads this version only.
const Version = 1

// MinPassphrase is the shortest passphrase Encode accepts.
const MinPassphrase = 12

// MaxFile is the largest backup file restored.
const MaxFile = 128 << 20

// MaxDecoded caps the decompressed size of a backup, against crafted files.
const MaxDecoded = 1 << 30

// Errors Decode returns for files it cannot use.
var (
	ErrNotBackup = errors.New("this is not an Eyrie backup file")
	ErrDecrypt   = errors.New("wrong passphrase, or the file is damaged")
)

// Meta describes a backup.
type Meta struct {
	AppVersion string    `json:"app_version"`
	Schema     int64     `json:"schema"` // the database migration the data matches
	Database   string    `json:"database"`
	Created    time.Time `json:"created"`
	// History is the history included: "none", "30d", "60d", "90d" or "all".
	History string `json:"history"`
}

// KDF holds the argon2id parameters of the file's key.
type KDF struct {
	Name    string `json:"name"` // "argon2id"
	Salt    []byte `json:"salt"`
	Time    uint32 `json:"time"`
	Memory  uint32 `json:"memory_kib"`
	Threads uint8  `json:"threads"`
}

// file is the JSON written to disk.
type file struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
	Meta    Meta   `json:"meta"` // for display; the encrypted copy is the one used
	KDF     KDF    `json:"kdf"`
	Nonce   []byte `json:"nonce"`
	Data    []byte `json:"data"` // AES-256-GCM of the gzipped payload
}

type payload struct {
	Meta Meta           `json:"meta"`
	Data store.Snapshot `json:"data"`
}

// Key derivation parameters of new files (RFC 9106's second recommended
// option), and the most Decode spends on a file it is handed.
const (
	kdfTime       = 3
	kdfMemory     = 64 * 1024
	kdfThreads    = 2
	maxKDFTime    = 10
	maxKDFMemory  = 256 * 1024
	maxKDFThreads = 8
)

// Encode writes snap as an encrypted backup file.
func Encode(w io.Writer, meta Meta, snap store.Snapshot, passphrase string) error {
	if len(passphrase) < MinPassphrase {
		return fmt.Errorf("the passphrase must be at least %d characters", MinPassphrase)
	}
	var plain bytes.Buffer
	zw := gzip.NewWriter(&plain)
	if err := json.NewEncoder(zw).Encode(payload{Meta: meta, Data: snap}); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	kdf := KDF{Name: "argon2id", Salt: make([]byte, 16), Time: kdfTime, Memory: kdfMemory, Threads: kdfThreads}
	rand.Read(kdf.Salt)
	aead, err := newAEAD(passphrase, kdf)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	rand.Read(nonce)
	f := file{
		Format: FormatName, Version: Version, Meta: meta, KDF: kdf, Nonce: nonce,
		Data: aead.Seal(nil, nonce, plain.Bytes(), []byte(FormatName)),
	}
	return json.NewEncoder(w).Encode(f)
}

// Decode reads an encrypted backup file.
func Decode(r io.Reader, passphrase string) (Meta, store.Snapshot, error) {
	var f file
	if err := json.NewDecoder(io.LimitReader(r, MaxFile)).Decode(&f); err != nil || f.Format != FormatName {
		return Meta{}, store.Snapshot{}, ErrNotBackup
	}
	if f.Version != Version {
		return Meta{}, store.Snapshot{}, fmt.Errorf("backup format version %d is not supported by this version of Eyrie", f.Version)
	}
	k := f.KDF
	if k.Name != "argon2id" || len(k.Salt) < 16 || k.Time == 0 || k.Time > maxKDFTime ||
		k.Memory < 8*uint32(k.Threads) || k.Memory > maxKDFMemory || k.Threads == 0 || k.Threads > maxKDFThreads {
		return Meta{}, store.Snapshot{}, ErrNotBackup
	}
	aead, err := newAEAD(passphrase, k)
	if err != nil {
		return Meta{}, store.Snapshot{}, err
	}
	if len(f.Nonce) != aead.NonceSize() {
		return Meta{}, store.Snapshot{}, ErrNotBackup
	}
	plain, err := aead.Open(nil, f.Nonce, f.Data, []byte(FormatName))
	if err != nil {
		return Meta{}, store.Snapshot{}, ErrDecrypt
	}
	zr, err := gzip.NewReader(bytes.NewReader(plain))
	if err != nil {
		return Meta{}, store.Snapshot{}, ErrDecrypt
	}
	dec := json.NewDecoder(io.LimitReader(zr, MaxDecoded))
	dec.UseNumber() // keep integers exact
	var p payload
	if err := dec.Decode(&p); err != nil {
		return Meta{}, store.Snapshot{}, fmt.Errorf("the backup's contents are damaged: %w", err)
	}
	if p.Data.Config == nil {
		return Meta{}, store.Snapshot{}, errors.New("the backup's contents are damaged: no configuration")
	}
	return p.Meta, p.Data, nil
}

func newAEAD(passphrase string, k KDF) (cipher.AEAD, error) {
	key := argon2.IDKey([]byte(passphrase), k.Salt, k.Time, k.Memory, k.Threads, 32)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// ErrHistoryRange is returned for a history choice HistoryRange does not
// know.
var ErrHistoryRange = errors.New("unknown history range")

// HistoryRange turns a choice ("none", "30", "60", "90" or "all") into what
// Export takes: whether to include history, the earliest time to include
// (zero for all) and the Meta label.
func HistoryRange(choice string, now time.Time) (include bool, since time.Time, label string, err error) {
	switch choice {
	case "", "none":
		return false, time.Time{}, "none", nil
	case "30", "60", "90":
		days := map[string]int{"30": 30, "60": 60, "90": 90}[choice]
		return true, now.AddDate(0, 0, -days), choice + "d", nil
	case "all":
		return true, time.Time{}, "all", nil
	}
	return false, time.Time{}, "", fmt.Errorf("%w %q", ErrHistoryRange, choice)
}

// Name is a backup file's name for its creation time.
func Name(created time.Time) string {
	return "eyrie-backup-" + created.UTC().Format("20060102-150405") + ".json"
}
