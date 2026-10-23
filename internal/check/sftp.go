// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"cmp"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// SFTP is the settings of the sftp type: Eyrie must sign in to an SFTP
// server and, if a path is set, find it there.
type SFTP struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	PrivateKey string `json:"private_key"` // PEM or OpenSSH format
	Passphrase string `json:"passphrase"`  // of the private key
	Path       string `json:"path"`
	// HostKey is the server key's SHA-256 fingerprint ("SHA256:…"); empty:
	// any key is accepted.
	HostKey string `json:"host_key"`
}

func (s *SFTP) secrets() map[string]*string {
	return map[string]*string{"password": &s.Password, "private_key": &s.PrivateKey, "passphrase": &s.Passphrase}
}

func (s *SFTP) normalize() error {
	h, err := host(s.Host)
	if err != nil {
		return err
	}
	s.Host = h
	if s.Port == 0 {
		s.Port = 22
	}
	s.Username = strings.TrimSpace(s.Username)
	if s.Username == "" {
		return errors.New("the user name is required")
	}
	if s.Password == "" && s.PrivateKey == "" {
		return errors.New("a password or a private key is required")
	}
	s.Path = strings.TrimSpace(s.Path)
	if len(s.Path) > 4096 {
		return errors.New("the path is too long")
	}
	s.HostKey = strings.TrimSpace(s.HostKey)
	if s.HostKey != "" && !strings.HasPrefix(s.HostKey, "SHA256:") {
		return errors.New("the host key must be a SHA-256 fingerprint (SHA256:…)")
	}
	return checkPort(s.Port)
}

func (s *SFTP) checker(env *Env) Checker { return &sftpChecker{s: s, env: env} }

func (s *SFTP) describe() []Fact {
	f := []Fact{{"Server", net.JoinHostPort(s.Host, strconv.Itoa(s.Port))}, {"User", s.Username}}
	if s.Path != "" {
		f = append(f, Fact{"Path", s.Path})
	}
	return f
}

type sftpChecker struct {
	s   *SFTP
	env *Env
}

func (c *sftpChecker) config() (*ssh.ClientConfig, *string, error) {
	s := c.s
	var auth []ssh.AuthMethod
	if s.PrivateKey != "" {
		var signer ssh.Signer
		var err error
		if s.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(s.PrivateKey), []byte(s.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(s.PrivateKey))
		}
		if err != nil {
			return nil, nil, fmt.Errorf("the private key cannot be read: %w", err)
		}
		auth = append(auth, ssh.PublicKeys(signer))
	}
	if s.Password != "" {
		auth = append(auth, ssh.Password(s.Password))
	}
	seen := new(string)
	return &ssh.ClientConfig{
		User: s.Username,
		Auth: auth,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			*seen = ssh.FingerprintSHA256(key)
			if s.HostKey != "" && *seen != s.HostKey {
				return fmt.Errorf("the server's host key is %s, not %s", *seen, s.HostKey)
			}
			return nil
		},
		ClientVersion: "SSH-2.0-Eyrie",
	}, seen, nil
}

func (c *sftpChecker) Check(ctx context.Context) Result {
	cfg, seen, err := c.config()
	if err != nil {
		return failErr(err)
	}
	addr := net.JoinHostPort(c.s.Host, strconv.Itoa(c.s.Port))
	start := time.Now()
	conn, err := c.env.Dial(ctx, "tcp", addr)
	if err != nil {
		return fail("%s", dialError(err))
	}
	defer conn.Close()
	conn.SetDeadline(deadline(ctx))                                         //nolint:errcheck,gosec // a failure shows as a timeout
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()) }) //nolint:errcheck,gosec // a failure shows as a timeout
	defer stop()
	sc, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		return failErr(connError(ctx, err))
	}
	client := ssh.NewClient(sc, chans, reqs)
	defer client.Close()
	msg, err := c.sftp(client)
	if err != nil {
		return failErr(connError(ctx, err))
	}
	if c.s.HostKey == "" {
		msg += "; host key " + *seen
	}
	return Result{OK: true, Message: msg, Latency: time.Since(start)}
}

// SFTP packets (draft-ietf-secsh-filexfer-02, version 3).
const (
	fxpInit    = 1
	fxpVersion = 2
	fxpStat    = 17
	fxpStatus  = 101
	fxpAttrs   = 105
)

var fxpErrors = map[uint32]string{2: "no such file", 3: "permission denied", 4: "failure"}

func (c *sftpChecker) sftp(client *ssh.Client) (string, error) {
	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()
	w, err := sess.StdinPipe()
	if err != nil {
		return "", err
	}
	r, err := sess.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := sess.RequestSubsystem("sftp"); err != nil {
		return "", fmt.Errorf("the server has no SFTP: %w", err)
	}
	if err := fxpWrite(w, fxpInit, binary.BigEndian.AppendUint32(nil, 3)); err != nil {
		return "", err
	}
	typ, body, err := fxpRead(r)
	if err != nil {
		return "", err
	}
	if typ != fxpVersion || len(body) < 4 {
		return "", errors.New("the server did not start SFTP")
	}
	msg := fmt.Sprintf("Signed in, SFTP version %d", binary.BigEndian.Uint32(body))
	if c.s.Path == "" {
		return msg, nil
	}
	req := binary.BigEndian.AppendUint32(nil, 1)
	req = binary.BigEndian.AppendUint32(req, uint32(len(c.s.Path))) //nolint:gosec // G115: at most 4096
	req = append(req, c.s.Path...)
	if err := fxpWrite(w, fxpStat, req); err != nil {
		return "", err
	}
	typ, body, err = fxpRead(r)
	if err != nil {
		return "", err
	}
	switch {
	case typ == fxpAttrs:
		return msg + "; " + c.s.Path + " exists", nil
	case typ == fxpStatus && len(body) >= 8:
		code := binary.BigEndian.Uint32(body[4:])
		return "", fmt.Errorf("%s: %s", c.s.Path, cmp.Or(fxpErrors[code], "error "+strconv.Itoa(int(code))))
	}
	return "", errors.New("the server's SFTP answer is not valid")
}

func fxpWrite(w io.Writer, typ byte, body []byte) error {
	pkt := binary.BigEndian.AppendUint32(nil, uint32(len(body)+1)) //nolint:gosec // G115: small packets
	pkt = append(append(pkt, typ), body...)
	_, err := w.Write(pkt)
	return err
}

func fxpRead(r io.Reader) (byte, []byte, error) {
	head := make([]byte, 5)
	if _, err := io.ReadFull(r, head); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(head)
	if n < 1 || n > 1<<20 {
		return 0, nil, errors.New("the server's SFTP packet is not valid")
	}
	body := make([]byte, n-1)
	_, err := io.ReadFull(r, body)
	return head[4], body, err
}
