// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zjcochran/eyrie/internal/workload"
)

// facts shows details as "Name: value" lines, for comparing.
func facts(f []Fact) string {
	var b strings.Builder
	for _, x := range f {
		b.WriteString(x.Name + ": " + x.Value + "\n")
	}
	return b.String()
}

func TestDrawbridgeDetails(t *testing.T) {
	srv, _ := fakeDrawbridge(t, http.StatusOK, `{"state":"failover","state_label":"Failover","since":"2026-10-14T15:17:34Z",
		"due":null,"current_isp":{"name":"SKYLINK"},"primary_isp":{"name":"EXAMPLENET"},"public_ip":"198.51.100.20",
		"last_check":"2026-10-14T15:18:30Z","paused":true,"stopped":["media-db-1","media-downloader-1"],"version":"0.13.0"}`)
	r := run(t, testEnv(), "drawbridge", drawbridgeSettings(srv.URL))
	want := "State: Failover\nISP in use: SKYLINK\nPrimary ISP: EXAMPLENET\nSince: 2026-10-14T15:17:34Z\nPublic IP: 198.51.100.20\n" +
		"Automation: Paused\nStopped containers: media-db-1, media-downloader-1\nLast WAN check: 2026-10-14T15:18:30Z\nDrawbridge version: 0.13.0\n"
	if got := facts(r.Details); got != want {
		t.Errorf("details:\n%s", got)
	}
}

func TestDockerDetails(t *testing.T) {
	started := time.Date(2027, 1, 11, 8, 30, 0, 0, time.UTC)
	got := facts(dockerDetails("/shop-web-1", "shop:1.4", 3, "running", true, false, 0, started, time.Time{}))
	if got != "State: Running\nContainer: shop-web-1\nImage: shop:1.4\nStarted: 2027-01-11T08:30:00Z\nRestarts: 3\n" {
		t.Errorf("running:\n%s", got)
	}
	got = facts(dockerDetails("", "", 0, "exited", false, true, 137, time.Time{}, started))
	if got != "State: Exited\nStopped: 2027-01-11T08:30:00Z\nExit code: 137\nOut of memory: Killed for running out of memory\nRestarts: 0\n" {
		t.Errorf("exited:\n%s", got)
	}
}

func TestWorkloadDetails(t *testing.T) {
	two, three := int64(2), int64(3)
	got := facts(workloadDetails(workload.Workload{Name: "shop_web", Kind: "service", Group: "shop", Status: "2 of 3 tasks running",
		Ready: &two, Wanted: &three}))
	if got != "State: 2 of 3 tasks running\nName: shop_web\nStack: shop\nReady: 2\nWanted: 3\n" {
		t.Errorf("service:\n%s", got)
	}
	if v := readyValue(workload.Workload{Ready: &two}); v == nil || *v != 2 {
		t.Errorf("ready value %v", v)
	}
	if readyValue(workload.Workload{}) != nil {
		t.Error("a guest has a ready value")
	}
	got = facts(workloadDetails(workload.Workload{Name: "media", Kind: "vm", Group: "pve1", Status: "Running",
		Guest: &workload.Guest{CPU: 0.125, Cores: 4, Memory: 2 << 30, MaxMem: 8 << 30, Disk: 10 << 30, MaxDisk: 32 << 30, Uptime: 3600}}))
	if got != "State: Running\nName: media\nNode: pve1\nKind: VM\nUp for: 1h0m0s\nCPU: 12.5% of 4 cores\n"+
		"Memory: 2.0 GiB of 8.0 GiB\nDisk: 10.0 GiB of 32.0 GiB\n" {
		t.Errorf("guest:\n%s", got)
	}
	for kind, want := range map[string]string{"container": "Compose project", "deployment": "Namespace", "lxc": "Node"} {
		if f := workloadDetails(workload.Workload{Kind: kind, Group: "g"}); f[2].Name != want {
			t.Errorf("%s: %v", kind, f)
		}
	}
}

func TestTLSDetails(t *testing.T) {
	if tlsDetails(nil) != nil {
		t.Error("details without TLS")
	}
	cert := &x509.Certificate{Subject: pkix.Name{CommonName: "shop.example.com"},
		Issuer: pkix.Name{Organization: []string{"Example CA"}, CommonName: "R1"}}
	got := facts(tlsDetails(&tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{cert}}))
	if got != "TLS: TLS 1.3\nCertificate for: shop.example.com\nIssued by: Example CA R1\n" {
		t.Errorf("TLS:\n%s", got)
	}
	cert.DNSNames = []string{"shop.example.com", "www.example.com"}
	if f := tlsDetails(&tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}}); f[1].Value != "shop.example.com, www.example.com" {
		t.Errorf("names: %v", f)
	}
}

func TestResponseDetails(t *testing.T) {
	u, _ := url.Parse("https://shop.example.com/home?token=x")
	resp := &http.Response{Status: "200 OK", Proto: "HTTP/2.0", Request: &http.Request{URL: u},
		Header: http.Header{"Content-Type": {"text/html"}, "Server": {"nginx"}}}
	got := facts(responseDetails("https://shop.example.com/", resp, 2048))
	if got != "Status: 200 OK\nRedirected to: https://shop.example.com/home\nProtocol: HTTP/2.0\nContent type: text/html\n"+
		"Body: 2.0 KiB\nServer: nginx\n" {
		t.Errorf("response:\n%s", got)
	}
}

func TestDetailHelpers(t *testing.T) {
	for n, want := range map[int]string{12: "12 bytes", 2048: "2.0 KiB", 3 << 20: "3.0 MiB"} {
		if got := byteSize(n); got != want {
			t.Errorf("byteSize(%d) = %s", n, got)
		}
	}
	for n, want := range map[int64]string{12: "12 bytes", 1536: "1.5 KiB", 5 << 40: "5.0 TiB"} {
		if got := byteSize64(n); got != want {
			t.Errorf("byteSize64(%d) = %s", n, got)
		}
	}
	if clipText("short", 10) != "short" || clipText("abcdef", 3) != "abc…" || clipText("aé", 2) != "a…" {
		t.Error("clipText")
	}
	if v := number(" 42.5 "); v == nil || *v != 42.5 {
		t.Errorf("number %v", v)
	}
	for _, s := range []string{"up", "NaN", "Inf", ""} {
		if number(s) != nil {
			t.Errorf("number(%q) is a number", s)
		}
	}
	if ntpReference(1, []byte("GPS\x00")) != "GPS" || ntpReference(2, []byte{192, 0, 2, 1}) != "192.0.2.1" {
		t.Error("ntpReference")
	}
	if got := fmt.Sprintf("%.3f", ntpShort([]byte{0, 0, 0x80, 0})); got != "500.000" {
		t.Errorf("ntpShort %s", got)
	}
	if got := ms(1500 * time.Microsecond); got != "1.5 ms" {
		t.Errorf("ms %s", got)
	}
	if rowCount(3) != "3" || rowCount(maxRows) != "1000 or more" {
		t.Error("rowCount")
	}
}

func TestUnitDetails(t *testing.T) {
	if got := facts(unitDetails("inactive", nil)); got != "State: Inactive\n" {
		t.Errorf("empty:\n%s", got)
	}
}
