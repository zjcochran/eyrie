// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"net"
	"reflect"
	"strings"
	"testing"
)

// switchAgent is an SNMP agent with three interfaces.
func switchAgent() *fakeAgent {
	a := newAgent()
	for k, v := range map[string][]byte{
		"1.3.6.1.2.1.1.1.0":           berString([]byte("Example switch OS 1.0")),
		"1.3.6.1.2.1.2.2.1.2.1":       berString([]byte("eth0")),
		"1.3.6.1.2.1.2.2.1.2.2":       berString([]byte("eth1")),
		"1.3.6.1.2.1.2.2.1.2.10":      berString([]byte("lo")),
		"1.3.6.1.2.1.2.2.1.7.1":       berInt(1),
		"1.3.6.1.2.1.2.2.1.7.2":       berInt(2),
		"1.3.6.1.2.1.2.2.1.7.10":      berInt(1),
		"1.3.6.1.2.1.2.2.1.8.1":       berInt(1),
		"1.3.6.1.2.1.2.2.1.8.2":       berInt(7),
		"1.3.6.1.2.1.2.2.1.8.10":      berInt(9),
		"1.3.6.1.2.1.31.1.1.1.1.1":    berString([]byte("ge-0/0/1")),
		"1.3.6.1.2.1.31.1.1.1.18.1":   berString([]byte("uplink")),
		"1.3.6.1.2.1.2.2.1.2.0":       berString([]byte("not an interface")),
		"1.3.6.1.2.1.31.1.1.1.18.999": berString([]byte("no such interface")),
	} {
		a.values[k] = v
	}
	return a
}

var wantSwitch = SNMPDevice{Name: "router", Description: "Example switch OS 1.0", Interfaces: []SNMPInterface{
	{Index: 1, Name: "ge-0/0/1", Alias: "uplink", Admin: "up", Oper: "up"},
	{Index: 2, Name: "eth1", Admin: "down", Oper: "lower layer down"},
	{Index: 10, Name: "lo", Admin: "up", Oper: "unknown"},
}}

func TestDiscoverSNMP(t *testing.T) {
	env := testEnv()
	a := switchAgent()
	var dialed string
	env.Dial = pipeDial(a.serve, &dialed)
	ctx := context.Background()
	for _, v := range []string{"1", "2c"} {
		d, err := DiscoverSNMP(ctx, env, []byte(`{"host":"switch.example.com","version":"`+v+`","oid":"x"}`))
		if err != nil || !reflect.DeepEqual(d, wantSwitch) {
			t.Errorf("version %s: %+v %v", v, d, err)
		}
	}
	if dialed != "udp switch.example.com:161" {
		t.Errorf("dialed %s", dialed)
	}

	// SNMPv3 discovers the engine once, and keeps up with its clock.
	a.user, a.authProto, a.authPass, a.privProto, a.privPass, a.staleOnce = "alice", "SHA", "authpass1", "AES", "privpass1", true
	d, err := DiscoverSNMP(ctx, env, []byte(`{"host":"switch.example.com","version":"3","username":"alice",`+
		`"auth_protocol":"SHA","auth_password":"authpass1","priv_protocol":"AES","priv_password":"privpass1"}`))
	if err != nil || !reflect.DeepEqual(d, wantSwitch) {
		t.Errorf("version 3: %+v %v", d, err)
	}

	// An agent with nothing to say.
	a = newAgent()
	a.errStatus = 2
	env.Dial = pipeDial(a.serve, nil)
	if d, err := DiscoverSNMP(ctx, env, []byte(`{"host":"switch.example.com"}`)); err != nil || d.Name != "" || len(d.Interfaces) != 0 {
		t.Errorf("empty: %+v %v", d, err)
	}
	a.errStatus = 5
	if _, err := DiscoverSNMP(ctx, env, []byte(`{"host":"switch.example.com"}`)); err == nil || err.Error() != "the agent answered genErr" {
		t.Errorf("error status: %v", err)
	}
	a.errStatus = 0
	tctx, cancel := context.WithTimeout(ctx, 0)
	defer cancel()
	if _, err := DiscoverSNMP(tctx, env, []byte(`{"host":"switch.example.com","community":"secret"}`)); err == nil ||
		!strings.Contains(err.Error(), "community") {
		t.Errorf("wrong community: %v", err)
	}

	for _, s := range []string{`[`, `{"host":""}`} {
		if _, err := DiscoverSNMP(ctx, env, []byte(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
	env.Dial = func(context.Context, string, string) (net.Conn, error) { return nil, context.DeadlineExceeded }
	if _, err := DiscoverSNMP(ctx, env, []byte(`{"host":"switch.example.com"}`)); err == nil || err.Error() != "Timed out" {
		t.Errorf("dial: %v", err)
	}
}
