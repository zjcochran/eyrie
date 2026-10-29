// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"fmt"
	"net/http"
	"testing"
)

func TestStatusChanges(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	bob, _ := e.addUser(admin, "bob", "viewer")
	a := create(t, admin, portMonitor("A"))
	b := create(t, admin, portMonitor("B"))
	var all []changeView
	eventually(t, "two status changes", func() bool {
		bob.call("GET", "/api/v1/status-changes", nil, &all)
		return len(all) == 2
	})
	if all[0].ID <= all[1].ID || all[0].Status != "up" || !all[0].Important || all[0].LatencyMS == nil {
		t.Errorf("changes %+v", all)
	}
	if ids := map[int64]bool{all[0].MonitorID: true, all[1].MonitorID: true}; !ids[a.ID] || !ids[b.ID] {
		t.Errorf("monitors %+v", all)
	}

	var page []changeView
	if bob.call("GET", "/api/v1/status-changes?limit=1", nil, &page); len(page) != 1 || page[0].ID != all[0].ID {
		t.Errorf("limit %+v", page)
	}
	if bob.call("GET", fmt.Sprintf("/api/v1/status-changes?limit=5000&before=%d", all[0].ID), nil, &page); len(page) != 1 || page[0].ID != all[1].ID {
		t.Errorf("before %+v", page)
	}
	for _, q := range []string{"before=0", "limit=x"} {
		if code := bob.call("GET", "/api/v1/status-changes?"+q, nil, nil); code != http.StatusBadRequest {
			t.Errorf("%s: %d", q, code)
		}
	}
	if code := e.client().call("GET", "/api/v1/status-changes", nil, nil); code != http.StatusUnauthorized {
		t.Errorf("signed out: %d", code)
	}
}
