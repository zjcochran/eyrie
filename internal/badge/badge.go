// SPDX-License-Identifier: AGPL-3.0-or-later

// Package badge draws status badges: small SVG images with a grey label on
// the left and a coloured message on the right, for README files and
// dashboards. Text widths are estimated from Verdana at 11 px, so no font
// is needed to draw them.
package badge

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"math"
	"strings"
)

// Colours of messages.
const (
	Green     = "#2e9e4f"
	LightGrn  = "#7ab317"
	Yellow    = "#c6a314"
	Orange    = "#e07b28"
	Red       = "#d9473a"
	Grey      = "#8b939c"
	Violet    = "#7a5af8"
	labelGrey = "#555555"
)

// Badge is a label and a message, with the message's colour.
type Badge struct {
	Label, Message, Color string
}

// width is the estimated width of text in Verdana 11 px.
func width(s string) float64 {
	w := 0.0
	for _, r := range s {
		switch {
		case strings.ContainsRune("iljI.,:;!|'` ", r):
			w += 3.6
		case strings.ContainsRune("frt()[]{}/\\-", r):
			w += 4.7
		case strings.ContainsRune("mwMW@%", r):
			w += 10.5
		case r >= 'A' && r <= 'Z':
			w += 7.6
		case r < 128:
			w += 6.6
		default:
			w += 8 // other scripts: a little wider, to be safe
		}
	}
	return math.Ceil(w)
}

func esc(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s)) //nolint:errcheck,gosec // a bytes.Buffer
	return b.String()
}

// SVG draws the badge.
func (b Badge) SVG() []byte {
	const pad = 6.0
	lw, mw := width(b.Label)+2*pad, width(b.Message)+2*pad
	total := lw + mw
	label, msg := esc(b.Label), esc(b.Message)
	var out bytes.Buffer
	fmt.Fprintf(&out, `<svg xmlns="http://www.w3.org/2000/svg" width="%g" height="20" role="img" aria-label="%s: %s">`, total, label, msg)
	fmt.Fprintf(&out, `<title>%s: %s</title>`, label, msg)
	out.WriteString(`<linearGradient id="g" x2="0" y2="100%"><stop offset="0" stop-color="#fff" stop-opacity=".12"/>` +
		`<stop offset="1" stop-opacity=".12"/></linearGradient>`)
	fmt.Fprintf(&out, `<clipPath id="c"><rect width="%g" height="20" rx="3"/></clipPath>`, total)
	fmt.Fprintf(&out, `<g clip-path="url(#c)"><rect width="%g" height="20" fill="%s"/><rect x="%g" width="%g" height="20" fill="%s"/>`+
		`<rect width="%g" height="20" fill="url(#g)"/></g>`, lw, labelGrey, lw, mw, esc(b.Color), total)
	out.WriteString(`<g fill="#fff" text-anchor="middle" font-family="Verdana,DejaVu Sans,sans-serif" font-size="11">`)
	for _, t := range []struct {
		x    float64
		text string
	}{{lw / 2, label}, {lw + mw/2, msg}} {
		fmt.Fprintf(&out, `<text x="%g" y="15" fill="#000" fill-opacity=".3">%s</text><text x="%g" y="14">%s</text>`, t.x, t.text, t.x, t.text)
	}
	out.WriteString(`</g></svg>`)
	return out.Bytes()
}

// UptimeColor is the colour of an uptime share from 0 to 1.
func UptimeColor(share float64) string {
	switch {
	case share >= 0.999:
		return Green
	case share >= 0.99:
		return LightGrn
	case share >= 0.95:
		return Yellow
	case share >= 0.9:
		return Orange
	}
	return Red
}
