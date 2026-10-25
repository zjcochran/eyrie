// SPDX-License-Identifier: AGPL-3.0-or-later

// Package workload lists what runs on a platform, and whether each thing is
// up: Docker containers, Docker Swarm services, Kubernetes workloads and
// Proxmox VE guests. Eyrie's agent reports these lists to Eyrie, where
// agent monitors read them. Every client only reads.
package workload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Workload is one thing that runs, as an agent reports it.
type Workload struct {
	// Key names the workload on its agent, and stays the same when it is
	// re-created: a container's name, service/<name>, <namespace>/<kind>/<name>
	// or lxc/<vmid>.
	Key   string `json:"key"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`            // container, service, deployment, statefulset, daemonset, lxc, vm
	Group string `json:"group,omitempty"` // the Compose project, stack, namespace or Proxmox node
	Up    bool   `json:"up"`
	// Status says how it is, as a sentence: "Running, healthy", "2 of 3
	// ready".
	Status string `json:"status"`
}

// Lister lists a platform's workloads.
type Lister interface {
	List(ctx context.Context) ([]Workload, error)
}

// MaxBody is the most read from an API's answer.
const MaxBody = 32 << 20

// sentence starts s with a capital letter.
func sentence(s string) string {
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// ready is the status of something with replicas: up when every one it
// wants is ready, down when it wants none.
func ready(have, want int64, noun string) (bool, string) {
	if want <= 0 {
		return false, "Scaled to 0"
	}
	return have >= want, fmt.Sprintf("%d of %d %s", have, want, noun)
}

// apiError reads an API's error answer: its JSON message, or its text.
func apiError(name string, resp *http.Response, decode func([]byte) string) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	msg := decode(b)
	if msg == "" {
		msg = strings.TrimSpace(string(b))
	}
	if msg == "" {
		return fmt.Errorf("%s answered %s", name, resp.Status)
	}
	return fmt.Errorf("%s answered %s: %s", name, resp.Status, msg)
}

// errNoBody is a successful answer without the JSON expected.
var errNoBody = errors.New("the answer is not JSON")
