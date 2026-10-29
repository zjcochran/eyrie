// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/zjcochran/eyrie/internal/agentauth"
	"github.com/zjcochran/eyrie/internal/check"
)

func TestJobs(t *testing.T) {
	e, a := setup(t)
	l := &lister{}
	a.Env = &check.Env{Workloads: l, Dial: func(context.Context, string, string) (net.Conn, error) { return nil, net.ErrClosed },
		Transport: func(bool) http.RoundTripper { return http.DefaultTransport }}
	e.jobs = []agentauth.Job{
		{ID: "1", Monitor: 1, Type: "docker-swarm", Settings: json.RawMessage(`{"service":"api"}`), TimeoutMS: 1000},
		{ID: "2", Monitor: 2, Type: "push", Settings: json.RawMessage(`{}`)},
		{ID: "3", Monitor: 3, Type: "telepathy"},
		{ID: "4", Monitor: 4, Type: "docker", Settings: json.RawMessage(`{"container":"web"}`)},
	}
	if err := runUntil(t, a, func() bool { return len(e.got()) == 4 }); err != nil {
		t.Fatal(err)
	}
	got := e.got()
	sort.Slice(got, func(i, j int) bool { return got[i].ID < got[j].ID })
	want := []string{"No service api", "The agent cannot check this type of monitor",
		`The agent cannot check this monitor: unknown monitor type "telepathy"`, "Cannot reach Docker: "}
	for i, r := range got {
		if r.OK || !strings.HasPrefix(r.Message, want[i]) || r.LatencyUS != -1 || r.CertExpires != nil {
			t.Errorf("result %d: %+v", i+1, r)
		}
	}
}

func TestRunJobResult(t *testing.T) {
	e, a := setup(t)
	if err := runUntil(t, a, func() bool { return e.count() > 0 }); err != nil {
		t.Fatal(err)
	}
	expires := time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC)
	a.Env = &check.Env{}
	ctx := context.Background()
	a.send1(ctx, "9", check.Result{OK: true, Message: "OK", Latency: 1500 * time.Microsecond, CertExpires: expires,
		Usage: &check.Usage{CPU: 5, Memory: 60}})
	got := e.got()
	if len(got) != 1 || !got[0].OK || got[0].LatencyUS != 1500 || got[0].CertExpires == nil || !got[0].CertExpires.Equal(expires) ||
		got[0].Usage == nil || *got[0].Usage != (agentauth.Usage{CPU: 5, Memory: 60}) {
		t.Errorf("sent %+v", got)
	}
}
