// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type auditPage struct {
	Entries []auditView `json:"entries"`
	Actions []string    `json:"actions"`
	Users   []string    `json:"users"`
}

func TestAudit(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	viewer, _ := e.addUser(admin, "bob", "viewer")
	admin.call("POST", "/api/v1/tags", map[string]string{"name": "prod", "color": "#1f5fbf"}, nil)
	e.client().do("POST", "/api/v1/login", credentials{"bob", "a wrong password"})

	var p auditPage
	if code := admin.call("GET", "/api/v1/audit", nil, &p); code != http.StatusOK {
		t.Fatalf("audit: %d", code)
	}
	var got []string
	for _, x := range p.Entries {
		got = append(got, x.User+": "+x.Action+" "+x.Details)
	}
	want := []string{"bob: failed sign-in ", "alice: tag created tag=prod", "bob: signed in ", "alice: user created user=bob role=viewer",
		"alice: signed in ", "alice: first admin created "}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("entries\n got %q\nwant %q", got, want)
	}
	if x := p.Entries[0]; x.IP == "" || x.At.IsZero() || x.At.Location() != time.UTC {
		t.Errorf("entry %+v", x)
	}
	if strings.Join(p.Users, ",") != "alice,bob" || len(p.Actions) != 5 {
		t.Errorf("filters %v %v", p.Users, p.Actions)
	}

	// Filters and pages.
	admin.call("GET", "/api/v1/audit?action=signed+in&user=bob", nil, &p)
	if len(p.Entries) != 1 || p.Entries[0].User != "bob" {
		t.Errorf("filtered %+v", p.Entries)
	}
	admin.call("GET", "/api/v1/audit?limit=2", nil, &p)
	if len(p.Entries) != 2 {
		t.Fatalf("limit: %d", len(p.Entries))
	}
	var older auditPage
	admin.call("GET", fmt.Sprintf("/api/v1/audit?limit=2000&before=%d", p.Entries[1].ID), nil, &older)
	if len(older.Entries) != 4 || older.Entries[0].Action != "signed in" {
		t.Errorf("older %+v", older.Entries)
	}
	for _, q := range []string{"before=x", "limit=0"} {
		if code, _ := admin.errorOf("GET", "/api/v1/audit?"+q, nil); code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, code)
		}
	}
	if code, _ := viewer.errorOf("GET", "/api/v1/audit", nil); code != http.StatusForbidden {
		t.Errorf("viewer: %d", code)
	}

	// A broken database still lets the action through.
	e.st.Close()
	e.s.audit(httptest.NewRequest(http.MethodPost, "/", nil), "tag created", "tag", "x", "by", "alice")
	if !strings.Contains(e.log.String(), "cannot write the audit log") {
		t.Error("audit failure not logged")
	}
}

func TestDetail(t *testing.T) {
	when := time.Date(2027, 1, 15, 8, 0, 0, 0, time.FixedZone("x", 3600))
	for v, want := range map[any]string{"plain": "plain", "two words": `"two words"`, "": `""`, 3: "3", true: "true", when: "2027-01-15T07:00:00Z"} {
		if got := detail(v); got != want {
			t.Errorf("%v: %q", v, got)
		}
	}
}
