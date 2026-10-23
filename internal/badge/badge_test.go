// SPDX-License-Identifier: AGPL-3.0-or-later

package badge

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestSVG(t *testing.T) {
	svg := string(Badge{Label: "Shop <web>", Message: "up & running", Color: Green}.SVG())
	// It is well-formed XML, with the text escaped.
	var v struct{}
	if err := xml.Unmarshal([]byte(svg), &v); err != nil {
		t.Fatalf("%v\n%s", err, svg)
	}
	for _, want := range []string{`<title>Shop &lt;web&gt;: up &amp; running</title>`, `fill="#2e9e4f"`, `height="20"`,
		`aria-label="Shop &lt;web&gt;: up &amp; running"`} {
		if !strings.Contains(svg, want) {
			t.Errorf("missing %q in %s", want, svg)
		}
	}
	// Longer text is wider.
	if width("uptime 30d") <= width("up") || width("WWW") <= width("iii") || width("日本") != 16 {
		t.Error("widths")
	}
}

func TestUptimeColor(t *testing.T) {
	for share, want := range map[float64]string{1: Green, 0.995: LightGrn, 0.96: Yellow, 0.91: Orange, 0.5: Red} {
		if got := UptimeColor(share); got != want {
			t.Errorf("%v: %s, want %s", share, got, want)
		}
	}
}
