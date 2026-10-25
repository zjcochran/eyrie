// SPDX-License-Identifier: AGPL-3.0-or-later

package workload

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Proxmox lists a Proxmox VE cluster's LXC containers and VMs, through its
// API with an API token. Proxmox's certificates are usually self-signed,
// so the server's certificate is pinned by its SHA-256 fingerprint instead
// of checked against a CA.
type Proxmox struct {
	base  string // https://host:8006/api2/json
	token string // the Authorization header
	http  *http.Client
}

// Fingerprint formats a certificate's SHA-256 as Proxmox shows it:
// upper-case hex pairs separated by colons.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	pairs := make([]string, len(sum))
	for i, b := range sum {
		pairs[i] = strings.ToUpper(hex.EncodeToString([]byte{b}))
	}
	return strings.Join(pairs, ":")
}

// plainFingerprint makes fingerprints comparable however they are written.
func plainFingerprint(fp string) string {
	return strings.ToUpper(strings.NewReplacer(":", "", " ", "").Replace(strings.TrimSpace(fp)))
}

// NewProxmox returns a client for the API at rawURL, such as
// https://pve.example.com:8006. tokenID is user@realm!name. The server's
// certificate must have the SHA-256 fingerprint fp; without one, every
// request fails with the fingerprint the server presented.
func NewProxmox(rawURL, tokenID, secret, fp string) (*Proxmox, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(rawURL), "/"))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("PROXMOX_URL must be https://<host>:8006")
	}
	if !strings.Contains(tokenID, "!") || !strings.Contains(tokenID, "@") || secret == "" {
		return nil, errors.New("PROXMOX_TOKEN_ID must be user@realm!name, with PROXMOX_TOKEN_SECRET")
	}
	want := plainFingerprint(fp)
	t := &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS12,
		// The certificate is checked by its fingerprint below, on every
		// connection, resumed ones included.
		InsecureSkipVerify: true, //nolint:gosec // G402: pinned by fingerprint in VerifyConnection
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("proxmox: no certificate")
			}
			got := Fingerprint(cs.PeerCertificates[0].Raw)
			if want == "" {
				return fmt.Errorf("PROXMOX_FINGERPRINT is not set; the server's certificate has %s: check it in the Proxmox web UI (node → System → Certificates) and set it", got)
			}
			if plainFingerprint(got) != want {
				return fmt.Errorf("the Proxmox server's certificate fingerprint is %s, not PROXMOX_FINGERPRINT", got)
			}
			return nil
		},
	}}
	return &Proxmox{
		base: u.String() + "/api2/json", token: "PVEAPIToken=" + tokenID + "=" + secret,
		http: &http.Client{Transport: t, Timeout: 30 * time.Second},
	}, nil
}

// List returns the cluster's LXC containers and VMs, templates left out.
func (p *Proxmox) List(ctx context.Context) ([]Workload, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.base+"/cluster/resources?type=vm", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", p.token)
	resp, err := p.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("cannot reach Proxmox: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apiError("Proxmox", resp, func([]byte) string { return "" })
	}
	var body struct {
		Data []struct {
			Type     string `json:"type"` // lxc or qemu
			Node     string `json:"node"`
			VMID     int    `json:"vmid"`
			Name     string `json:"name"`
			Status   string `json:"status"`
			Template int    `json:"template"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, MaxBody)).Decode(&body); err != nil {
		return nil, fmt.Errorf("proxmox: %w", errNoBody)
	}
	out := []Workload{}
	for _, g := range body.Data {
		if g.Template == 1 || (g.Type != "lxc" && g.Type != "qemu") {
			continue
		}
		kind := map[string]string{"lxc": "lxc", "qemu": "vm"}[g.Type]
		id := strconv.Itoa(g.VMID)
		w := Workload{Key: g.Type + "/" + id, Name: g.Name, Kind: kind, Group: g.Node, Up: g.Status == "running"}
		if w.Name == "" {
			w.Name = id
		}
		w.Status = sentence(g.Status)
		if w.Status == "" {
			w.Status = "Unknown"
		}
		out = append(out, w)
	}
	sortWorkloads(out)
	return out, nil
}
