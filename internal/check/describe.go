// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"encoding/json"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Fact is one setting of a monitor, named for people: {"URL",
// "https://shop.example.com/health"}.
type Fact struct {
	Name, Value string
}

// Describe lists the settings of a monitor that may be shown outside
// Eyrie, to Claude for alert analysis. It leaves out anything that can be a
// credential: header values, request bodies, a URL's user, password, query
// and fragment. Saved settings that do not parse give nothing.
func Describe(typ string, raw json.RawMessage) []Fact {
	t, ok := Types[typ]
	if !ok {
		return nil
	}
	s := t.settings()
	if json.Unmarshal(raw, s) != nil {
		return nil
	}
	return s.describe()
}

// publicURL is a URL without its user, password, query and fragment.
func publicURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.User, u.RawQuery, u.ForceQuery, u.Fragment, u.RawFragment = nil, "", false, "", ""
	return u.String()
}

func (h *HTTP) describe() []Fact {
	f := []Fact{{"URL", publicURL(h.URL)}, {"Method", h.Method}, {"Accepted status codes", strings.Join(h.AcceptedStatusCodes, ", ")}}
	if h.MaxRedirects != nil {
		f = append(f, Fact{"Maximum redirects", strconv.Itoa(*h.MaxRedirects)})
	}
	if h.IgnoreTLS {
		f = append(f, Fact{"TLS errors", "ignored"})
	}
	if len(h.Headers) > 0 {
		names := make([]string, 0, len(h.Headers))
		for k := range h.Headers {
			names = append(names, k)
		}
		slices.Sort(names)
		f = append(f, Fact{"Request headers (values not shown)", strings.Join(names, ", ")})
	}
	if h.Body != "" {
		f = append(f, Fact{"Request body", "set (not shown)"})
	}
	return f
}

func (k *Keyword) describe() []Fact {
	want := "must contain"
	if k.InvertKeyword {
		want = "must not contain"
	}
	return append(k.HTTP.describe(), Fact{"Response " + want, strconv.Quote(k.Keyword)})
}

func (j *JSONQuery) describe() []Fact {
	return append(j.HTTP.describe(), Fact{"JSON query", j.JSONPath + " " + j.JSONOperator + " " + strconv.Quote(j.ExpectedValue)})
}

func (p *Port) describe() []Fact {
	return []Fact{{"Address", net.JoinHostPort(p.Host, strconv.Itoa(p.Port))}}
}

func (p *Ping) describe() []Fact {
	return []Fact{{"Host", p.Host}, {"Pings per check", strconv.Itoa(p.Count)}, {"Packet size", strconv.Itoa(p.PacketSize) + " bytes"}}
}

func (d *DNS) describe() []Fact {
	f := []Fact{{"Name", d.Host}, {"Record type", d.RecordType},
		{"DNS server", net.JoinHostPort(d.Resolver, strconv.Itoa(d.ResolverPort))}}
	if d.Expected != "" {
		f = append(f, Fact{"Expected in a record", strconv.Quote(d.Expected)})
	}
	return f
}
