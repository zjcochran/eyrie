// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"bufio"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// eventually retries cond for up to 5 seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for range 100 {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s did not happen", what)
}

func portMonitor(name string) map[string]any {
	return map[string]any{"name": name, "type": "port", "settings": map[string]any{"host": "db.example.com", "port": 5432}}
}

// create makes a monitor as admin and returns it.
func create(t *testing.T, admin *client, body map[string]any) monitorView {
	t.Helper()
	var m monitorView
	if code, msg := admin.errorOf("POST", "/api/v1/monitors", body); code != http.StatusCreated {
		t.Fatalf("create %v: %d %s", body, code, msg)
	}
	var list []monitorView
	admin.call("GET", "/api/v1/monitors", nil, &list)
	for _, v := range list {
		if v.Name == body["name"] {
			m = v
		}
	}
	return m
}

// statusOf reads a monitor's status.
func statusOf(c *client, id int64) string {
	var m monitorView
	c.call("GET", fmt.Sprintf("/api/v1/monitors/%d", id), nil, &m)
	return m.Status.Status
}

func TestCreateMonitorDefaults(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	var m monitorView
	if code := admin.call("POST", "/api/v1/monitors", portMonitor("DB"), &m); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	if m.Interval != 60 || m.RetryInterval != 60 || m.Timeout != 48 || !m.Active || m.Retries != 0 || m.GroupID != nil ||
		string(m.Settings) != `{"host":"db.example.com","port":5432}` || m.PushURL != "" {
		t.Errorf("defaults %+v", m)
	}
	eventually(t, "the first check", func() bool { return statusOf(admin, m.ID) == "up" })
	var got monitorView
	admin.call("GET", fmt.Sprintf("/api/v1/monitors/%d", m.ID), nil, &got)
	if got.Status.Message != "Connected" || got.Status.LastCheck == nil || got.Status.LatencyMS == nil || got.Status.Since == nil {
		t.Errorf("status %+v", got.Status)
	}
	if !strings.Contains(e.log.String(), "monitor created") {
		t.Error("not logged")
	}
}

func TestMonitorValidation(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	var group groupView
	admin.call("POST", "/api/v1/groups", map[string]any{"name": "Site"}, &group)
	for _, c := range []struct {
		change map[string]any
		want   string
	}{
		{map[string]any{"name": " "}, "name"},
		{map[string]any{"type": "telepathy"}, "type must be one of"},
		{map[string]any{"interval": 10}, "interval"},
		{map[string]any{"interval": 60.5}, "interval"},
		{map[string]any{"retry_interval": 100000}, "retry interval"},
		{map[string]any{"timeout": 120}, "timeout"},
		{map[string]any{"retries": -1}, "retries"},
		{map[string]any{"resend_every": 10001}, "resend"},
		{map[string]any{"description": strings.Repeat("x", 2001)}, "description"},
		{map[string]any{"settings": map[string]any{"host": "db.example.com", "port": 0}}, "port"},
		{map[string]any{"type": "group"}, "type must be one of"},
		{map[string]any{"group_id": 9999}, "no group"},
	} {
		body := portMonitor("X")
		for k, v := range c.change {
			body[k] = v
		}
		if code, msg := admin.errorOf("POST", "/api/v1/monitors", body); code != http.StatusUnprocessableEntity ||
			!strings.Contains(strings.ToLower(msg), c.want) {
			t.Errorf("%v: %d %q", c.change, code, msg)
		}
	}
	body := portMonitor("In the group")
	body["group_id"] = group.ID
	if m := create(t, admin, body); m.GroupID == nil || *m.GroupID != group.ID {
		t.Errorf("group %+v", m.GroupID)
	}
}

func TestUpdateMonitor(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	m := create(t, admin, portMonitor("DB"))
	path := fmt.Sprintf("/api/v1/monitors/%d", m.ID)
	change := portMonitor("Database")
	change["interval"], change["retries"], change["upside_down"] = 120, 2, true
	var got monitorView
	if code := admin.call("PUT", path, change, &got); code != http.StatusOK || got.Name != "Database" || got.Interval != 120 ||
		got.Retries != 2 || !got.UpsideDown {
		t.Fatalf("update: %d %+v", code, got)
	}
	change["type"] = "ping"
	if code, msg := admin.errorOf("PUT", path, change); code != http.StatusUnprocessableEntity || !strings.Contains(msg, "type") {
		t.Errorf("type change: %d %q", code, msg)
	}
	change["type"], change["name"] = "port", ""
	if code, _ := admin.errorOf("PUT", path, change); code != http.StatusUnprocessableEntity {
		t.Errorf("no name: %d", code)
	}
	change["name"], change["group_id"] = "Database", 9999
	if code, _ := admin.errorOf("PUT", path, change); code != http.StatusUnprocessableEntity {
		t.Errorf("into no group: %d", code)
	}
	if code := admin.call("PUT", "/api/v1/monitors/9999", portMonitor("x"), nil); code != http.StatusNotFound {
		t.Errorf("unknown: %d", code)
	}
}

func TestPauseCheckDelete(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	m := create(t, admin, portMonitor("DB"))
	path := fmt.Sprintf("/api/v1/monitors/%d", m.ID)
	var got monitorView
	if code := admin.call("POST", path+"/pause", nil, &got); code != http.StatusOK || got.Active {
		t.Errorf("pause: %d %+v", code, got.Active)
	}
	if code, msg := admin.errorOf("POST", path+"/check", nil); code != http.StatusUnprocessableEntity || !strings.Contains(msg, "paused") {
		t.Errorf("check paused: %d %q", code, msg)
	}
	if code := admin.call("POST", path+"/resume", nil, &got); code != http.StatusOK || !got.Active {
		t.Errorf("resume: %d", code)
	}
	if code := admin.call("POST", path+"/check", nil, nil); code != http.StatusAccepted {
		t.Errorf("check: %d", code)
	}
	if code := admin.call("DELETE", path, nil, nil); code != http.StatusNoContent {
		t.Errorf("delete: %d", code)
	}
	for _, r := range [][2]string{{"DELETE", path}, {"GET", path}, {"POST", path + "/pause"}, {"POST", path + "/check"}} {
		if code := admin.call(r[0], r[1], nil, nil); code != http.StatusNotFound {
			t.Errorf("%s %s after delete: %d", r[0], r[1], code)
		}
	}
}

func TestMonitorAccess(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	m := create(t, admin, portMonitor("DB"))
	bob, _ := e.addUser(admin, "bob", "viewer")
	var list []monitorView
	if code := bob.call("GET", "/api/v1/monitors", nil, &list); code != http.StatusOK || len(list) != 1 {
		t.Errorf("viewer list: %d %+v", code, list)
	}
	if code := bob.call("POST", "/api/v1/monitors", portMonitor("x"), nil); code != http.StatusForbidden {
		t.Errorf("viewer create: %d", code)
	}
	var tok newToken
	admin.call("POST", "/api/v1/tokens", map[string]string{"name": "ci"}, &tok)
	reader := e.client()
	if resp, _ := reader.do("GET", fmt.Sprintf("/api/v1/monitors/%d", m.ID), nil, "Authorization", "Bearer "+tok.Token); resp.StatusCode != http.StatusOK {
		t.Errorf("token read: %d", resp.StatusCode)
	}
	if resp, _ := reader.do("DELETE", fmt.Sprintf("/api/v1/monitors/%d", m.ID), nil, "Authorization", "Bearer "+tok.Token); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("token delete: %d", resp.StatusCode)
	}
	if code := e.client().call("GET", "/api/v1/monitors", nil, nil); code != http.StatusUnauthorized {
		t.Errorf("signed out: %d", code)
	}
	var types []struct {
		Name, Label string
		Agents      bool
		AgentOnly   bool `json:"agent_only"`
	}
	if bob.call("GET", "/api/v1/monitor-types", nil, &types); len(types) != 37 || types[0].Name != "dns" || !types[0].Agents || types[0].AgentOnly {
		t.Errorf("types %+v", types)
	}
}

func TestHistory(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	m := create(t, admin, portMonitor("DB"))
	path := fmt.Sprintf("/api/v1/monitors/%d", m.ID)
	var beats []beatView
	eventually(t, "a saved heartbeat", func() bool {
		admin.call("GET", path+"/heartbeats", nil, &beats)
		return len(beats) == 1
	})
	if b := beats[0]; b.Status != "up" || !b.Important || b.LatencyMS == nil || b.MonitorID != m.ID {
		t.Errorf("beat %+v", b)
	}
	if admin.call("GET", path+"/heartbeats?since=2100-01-01T00:00:00Z&limit=5", nil, &beats); len(beats) != 0 {
		t.Errorf("future: %+v", beats)
	}
	if code := admin.call("GET", path+"/heartbeats?since=yesterday", nil, nil); code != http.StatusBadRequest {
		t.Errorf("bad since: %d", code)
	}
	var hours []struct {
		Up, Down     int64
		AvgLatencyMS *float64 `json:"avg_latency_ms"`
	}
	if admin.call("GET", path+"/hours?days=99", nil, &hours); len(hours) != 1 || hours[0].Up != 1 || hours[0].AvgLatencyMS == nil {
		t.Errorf("hours %+v", hours)
	}
	var list []monitorView
	admin.call("GET", "/api/v1/monitors?beats=10", nil, &list)
	if len(list) != 1 || len(list[0].Beats) != 1 || list[0].Uptime.Day == nil || *list[0].Uptime.Day != 1 || list[0].Uptime.Year == nil {
		t.Errorf("list with beats %+v", list)
	}
	for _, p := range []string{"/heartbeats", "/hours"} {
		if code := admin.call("GET", "/api/v1/monitors/9999"+p, nil, nil); code != http.StatusNotFound {
			t.Errorf("%s of unknown: %d", p, code)
		}
	}
}

func TestPushRoute(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	m := create(t, admin, map[string]any{"name": "Backup", "type": "push"})
	if !strings.HasPrefix(m.PushURL, e.srv.URL+"/api/push/") || !strings.HasSuffix(m.PushURL, "?status=up&msg=OK&ping=") {
		t.Fatalf("push URL %q", m.PushURL)
	}
	token := strings.TrimSuffix(strings.TrimPrefix(m.PushURL, e.srv.URL+"/api/push/"), "?status=up&msg=OK&ping=")
	script := e.client()
	if resp, body := script.do("GET", "/api/push/"+token+"?status=up&msg=done&ping=12.5", nil); resp.StatusCode != http.StatusOK ||
		string(body) != "{\"ok\":true}\n" {
		t.Errorf("push: %d %s", resp.StatusCode, body)
	}
	eventually(t, "the push", func() bool { return statusOf(admin, m.ID) == "up" })
	// POST from another origin works too: the token is the credential.
	if resp, _ := script.do("POST", "/api/push/"+token+"?status=down&msg="+strings.Repeat("x", 600), nil,
		"Sec-Fetch-Site", "cross-site"); resp.StatusCode != http.StatusOK {
		t.Errorf("cross-site push: %d", resp.StatusCode)
	}
	eventually(t, "the down push", func() bool { return statusOf(admin, m.ID) == "down" })
	if resp, body := script.do("GET", "/api/push/nope", nil); resp.StatusCode != http.StatusNotFound || !strings.Contains(string(body), `"ok":false`) {
		t.Errorf("unknown token: %d %s", resp.StatusCode, body)
	}
	admin.call("POST", fmt.Sprintf("/api/v1/monitors/%d/pause", m.ID), nil, nil)
	if resp, _ := script.do("GET", "/api/push/"+token, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("paused: %d", resp.StatusCode)
	}
	// Viewers and tokens do not see the URL: it can change the status.
	bob, _ := e.addUser(admin, "bob", "viewer")
	var seen monitorView
	if bob.call("GET", fmt.Sprintf("/api/v1/monitors/%d", m.ID), nil, &seen); seen.PushURL != "" {
		t.Errorf("a viewer sees the push URL")
	}
	// A new token, on request.
	var got monitorView
	body := map[string]any{"name": "Backup", "reset_push_token": true}
	if admin.call("PUT", fmt.Sprintf("/api/v1/monitors/%d", m.ID), body, &got); got.PushURL == m.PushURL || got.PushURL == "" {
		t.Errorf("reset token %q", got.PushURL)
	}
}

func TestManualStatus(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	m := create(t, admin, map[string]any{"name": "Office power", "type": "manual"})
	path := fmt.Sprintf("/api/v1/monitors/%d/status", m.ID)
	if code := admin.call("POST", path, map[string]string{"status": "down", "message": "outage"}, nil); code != http.StatusOK {
		t.Fatalf("set: %d", code)
	}
	eventually(t, "the manual status", func() bool { return statusOf(admin, m.ID) == "down" })
	if code, _ := admin.errorOf("POST", path, map[string]string{"status": "sideways"}); code != http.StatusUnprocessableEntity {
		t.Errorf("bad status: %d", code)
	}
	db := create(t, admin, portMonitor("DB"))
	if code, _ := admin.errorOf("POST", fmt.Sprintf("/api/v1/monitors/%d/status", db.ID), map[string]string{"status": "up"}); code != http.StatusUnprocessableEntity {
		t.Errorf("set a port monitor: %d", code)
	}
	if code := admin.call("POST", path, []byte("{"), nil); code != http.StatusBadRequest {
		t.Errorf("bad JSON: %d", code)
	}
}

func TestEvents(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	req, _ := http.NewRequest(http.MethodGet, e.srv.URL+"/api/v1/events", nil)
	resp, err := admin.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content type %q", resp.Header.Get("Content-Type"))
	}
	lines := make(chan string, 100)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	create(t, admin, portMonitor("DB"))
	seen := map[string]bool{}
	deadline := time.After(5 * time.Second)
	for !seen["event: beat"] || !seen["event: monitors"] {
		select {
		case l := <-lines:
			seen[l] = true
			if strings.HasPrefix(l, "data: {\"monitor_id\"") && !strings.Contains(l, `"status":"up"`) {
				t.Errorf("beat %s", l)
			}
		case <-deadline:
			t.Fatalf("events seen: %v", seen)
		}
	}
}

func TestResourcesMonitor(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	m := create(t, admin, map[string]any{"name": "Host", "type": "system-resources", "settings": map[string]any{"cpu_max": 90}})
	eventually(t, "the first check", func() bool { return statusOf(admin, m.ID) == "up" })
	var got monitorView
	admin.call("GET", fmt.Sprintf("/api/v1/monitors/%d", m.ID), nil, &got)
	if u := got.Status.Usage; u == nil || *u != (usageView{CPU: 30, Memory: 50}) || got.Status.Message != "CPU 30.0%, memory 50.0% of 8.0 GiB" {
		t.Errorf("status %+v", got.Status)
	}
	var beats []beatView
	eventually(t, "the heartbeat saved", func() bool {
		admin.call("GET", fmt.Sprintf("/api/v1/monitors/%d/heartbeats", m.ID), nil, &beats)
		return len(beats) > 0
	})
	if beats[0].Usage == nil || beats[0].Usage.CPU != 30 {
		t.Errorf("beats %+v", beats)
	}
	var hours []struct {
		AvgUsage *usageView `json:"avg_usage"`
	}
	admin.call("GET", fmt.Sprintf("/api/v1/monitors/%d/hours?days=1", m.ID), nil, &hours)
	if len(hours) == 0 || hours[len(hours)-1].AvgUsage == nil || hours[len(hours)-1].AvgUsage.Memory != 50 {
		t.Errorf("hours %+v", hours)
	}
	_, body := e.client().do("GET", "/metrics", nil, "Authorization", "Bearer m3trics")
	for _, want := range []string{
		fmt.Sprintf(`eyrie_monitor_cpu_ratio{id="%d",monitor="Host",type="system-resources"} 0.3`, m.ID),
		fmt.Sprintf(`eyrie_monitor_memory_ratio{id="%d",monitor="Host",type="system-resources"} 0.5`, m.ID),
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestMetrics(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	m := create(t, admin, portMonitor("DB \"main\""))
	eventually(t, "the first check", func() bool { return statusOf(admin, m.ID) == "up" })
	create(t, admin, map[string]any{"name": "Backup", "type": "push"})
	c := e.client()
	if resp, _ := c.do("GET", "/metrics", nil, "Authorization", "Bearer wrong"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", resp.StatusCode)
	}
	resp, body := c.do("GET", "/metrics", nil, "Authorization", "Bearer m3trics")
	text := string(body)
	for _, want := range []string{
		`eyrie_build_info{version="v1.2.3"} 1`,
		fmt.Sprintf(`eyrie_monitor_status{id="%d",monitor="DB \"main\"",type="port"} 1`, m.ID),
		`eyrie_monitor_status{id="2",monitor="Backup",type="push"} -1`,
		fmt.Sprintf(`eyrie_monitor_checks_total{id="%d",monitor="DB \"main\"",type="port",result="up"} 1`, m.ID),
		"eyrie_monitor_response_seconds{", "eyrie_monitor_active{", "eyrie_checks_running ", "go_goroutines ",
		"process_start_time_seconds ",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/plain; version=0.0.4") {
		t.Errorf("metrics: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	e.s.MetricsToken = ""
	if resp, _ := c.do("GET", "/metrics", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("off: %d", resp.StatusCode)
	}
	if label("a\nb") != "a b" || float(0.25) != "0.25" {
		t.Error("helpers")
	}
}

func TestRetention(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	var r Retention
	if admin.call("GET", "/api/v1/settings/retention", nil, &r); r.HeartbeatDays != 90 {
		t.Errorf("default %+v", r)
	}
	for _, days := range []int{0, 3651} {
		if code, _ := admin.errorOf("PUT", "/api/v1/settings/retention", Retention{days}); code != http.StatusUnprocessableEntity {
			t.Errorf("%d days: %d", days, code)
		}
	}
	if code := admin.call("PUT", "/api/v1/settings/retention", Retention{30}, &r); code != http.StatusOK || r.HeartbeatDays != 30 {
		t.Errorf("save: %d %+v", code, r)
	}
	if admin.call("GET", "/api/v1/settings/retention", nil, &r); r.HeartbeatDays != 30 {
		t.Errorf("saved %+v", r)
	}
}

func TestMonitorSecrets(t *testing.T) {
	e := newEnv(t)
	admin := e.setup()
	settings := map[string]any{"host": "radius.example.com", "username": "alice", "secret": "s3cret", "password": "pw"}
	m := create(t, admin, map[string]any{"name": "RADIUS", "type": "radius", "settings": settings})
	if strings.Contains(string(m.Settings), "s3cret") || strings.Contains(string(m.Settings), "pw\"") ||
		strings.Join(m.SecretsSet, ",") != "password,secret" {
		t.Errorf("shown %s %v", m.Settings, m.SecretsSet)
	}
	saved, _ := e.st.MonitorByID(t.Context(), m.ID)
	if strings.Contains(string(saved.Config), "s3cret") || !strings.Contains(string(saved.Config), `"v1:`) {
		t.Errorf("not sealed: %s", saved.Config)
	}
	// Saving the settings as shown keeps the secrets; clear_secrets drops one.
	var got monitorView
	path := fmt.Sprintf("/api/v1/monitors/%d", m.ID)
	body := map[string]any{"name": "RADIUS", "settings": m.Settings}
	if code := admin.call("PUT", path, body, &got); code != http.StatusOK || strings.Join(got.SecretsSet, ",") != "password,secret" {
		t.Errorf("kept: %d %v", code, got.SecretsSet)
	}
	body["clear_secrets"] = []string{"password"}
	if code := admin.call("PUT", path, body, &got); code != http.StatusOK || strings.Join(got.SecretsSet, ",") != "secret" {
		t.Errorf("cleared: %d %v", code, got.SecretsSet)
	}
	body["clear_secrets"] = []string{"secret"}
	if code, msg := admin.errorOf("PUT", path, body); code != http.StatusUnprocessableEntity || !strings.Contains(msg, "shared secret") {
		t.Errorf("required: %d %s", code, msg)
	}
}
