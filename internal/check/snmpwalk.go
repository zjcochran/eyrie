// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
)

// Discovery reads what an SNMP agent says about its device: its name and
// description (SNMPv2-MIB), and its interfaces (IF-MIB's ifTable and
// ifXTable), walked with GetNextRequests.

// OIDs discovery reads.
const (
	OIDSysUpTime     = "1.3.6.1.2.1.1.3.0"
	oidSysDescr      = "1.3.6.1.2.1.1.1.0"
	oidSysName       = "1.3.6.1.2.1.1.5.0"
	oidIfDescr       = "1.3.6.1.2.1.2.2.1.2"
	oidIfAdminStatus = "1.3.6.1.2.1.2.2.1.7"
	// OIDIfOperStatus is an interface's state; its index follows.
	OIDIfOperStatus = "1.3.6.1.2.1.2.2.1.8"
	oidIfName       = "1.3.6.1.2.1.31.1.1.1.1"
	oidIfAlias      = "1.3.6.1.2.1.31.1.1.1.18"
)

// MaxInterfaces is the most interfaces discovery reads from a device.
const MaxInterfaces = 1000

// ifStatus names IF-MIB's interface states.
var ifStatus = map[string]string{"1": "up", "2": "down", "3": "testing", "4": "unknown", "5": "dormant", "6": "not present",
	"7": "lower layer down"}

// SNMPDevice is what an SNMP agent says about its device.
type SNMPDevice struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Interfaces  []SNMPInterface `json:"interfaces"`
}

// SNMPInterface is one of a device's interfaces.
type SNMPInterface struct {
	Index int64  `json:"index"`
	Name  string `json:"name"`            // ifName, or ifDescr without one
	Alias string `json:"alias,omitempty"` // the description set on the device
	Admin string `json:"admin"`           // up, down or testing
	Oper  string `json:"oper"`            // up, down, …
}

// DiscoverSNMP asks an SNMP agent about its device, with the settings of
// an snmp monitor; their OID, comparison and expected value are not used.
func DiscoverSNMP(ctx context.Context, env *Env, settings json.RawMessage) (SNMPDevice, error) {
	var s SNMP
	if err := json.Unmarshal(settings, &s); err != nil {
		return SNMPDevice{}, errors.New("the SNMP settings are not valid")
	}
	s.OID, s.Operator, s.Expected = OIDSysUpTime, "", ""
	if err := s.normalize(); err != nil {
		return SNMPDevice{}, err
	}
	conn, err := env.Dial(ctx, "udp", net.JoinHostPort(s.Host, strconv.Itoa(s.Port)))
	if err != nil {
		return SNMPDevice{}, errors.New(dialError(err))
	}
	defer conn.Close()
	conn.SetDeadline(deadline(ctx)) //nolint:errcheck,gosec // a failure shows as a timeout
	w := &walker{c: &snmpChecker{s: &s, env: env}, ctx: ctx, conn: conn}
	var d SNMPDevice
	if d.Name, err = w.get(oidSysName); err == nil {
		d.Description, err = w.get(oidSysDescr)
	}
	if err != nil {
		return d, err
	}
	d.Interfaces, err = w.interfaces()
	return d, err
}

// walker asks one agent, over one connection.
type walker struct {
	c    *snmpChecker
	ctx  context.Context
	conn net.Conn
}

// get reads one value; "" when the agent has none.
func (w *walker) get(oid string) (string, error) {
	o, _ := parseOID(oid)
	a, err := w.c.ask(w.ctx, w.conn, pduGet, o)
	if err != nil {
		return "", err
	}
	if a.errState != 0 {
		return "", nil
	}
	v, ok := snmpValue(a.valTag, a.value)
	if !ok {
		return "", nil
	}
	return v, nil
}

// walk reads the values under root, at most MaxInterfaces, by the rest of
// their OIDs: a table column's values by row index.
func (w *walker) walk(root string) (map[string]string, error) {
	out := map[string]string{}
	cur := root
	for len(out) < MaxInterfaces {
		o, _ := parseOID(cur)
		a, err := w.c.ask(w.ctx, w.conn, pduGetNext, o)
		if err != nil {
			return out, err
		}
		// SNMPv1 ends a walk with noSuchName, v2c and v3 with endOfMibView.
		if a.errState == 2 || a.valTag == berEndOfMIB {
			break
		}
		if a.errState != 0 {
			return out, fmt.Errorf("the agent answered %s", cmp.Or(snmpErrors[a.errState], "error "+strconv.FormatInt(a.errState, 10)))
		}
		idx, under := strings.CutPrefix(a.oid, root+".")
		if !under || !after(a.oid, cur) {
			break
		}
		v, _ := snmpValue(a.valTag, a.value)
		out[idx] = v
		cur = a.oid
	}
	return out, nil
}

// after reports whether OID a comes after b, as a walk must go.
func after(a, b string) bool {
	x, _ := parseOID(a)
	y, _ := parseOID(b)
	return slices.Compare(x, y) > 0
}

// interfaces reads the interface table, by index.
func (w *walker) interfaces() ([]SNMPInterface, error) {
	cols := map[string]map[string]string{}
	for _, oid := range []string{oidIfDescr, oidIfAdminStatus, OIDIfOperStatus, oidIfName, oidIfAlias} {
		col, err := w.walk(oid)
		if err != nil {
			return nil, err
		}
		cols[oid] = col
	}
	out := []SNMPInterface{}
	for idx, descr := range cols[oidIfDescr] {
		n, err := strconv.ParseInt(idx, 10, 64)
		if err != nil || n < 1 {
			continue
		}
		out = append(out, SNMPInterface{
			Index: n, Name: cmp.Or(cols[oidIfName][idx], descr), Alias: cols[oidIfAlias][idx],
			Admin: cmp.Or(ifStatus[cols[oidIfAdminStatus][idx]], "unknown"), Oper: cmp.Or(ifStatus[cols[OIDIfOperStatus][idx]], "unknown"),
		})
	}
	slices.SortFunc(out, func(a, b SNMPInterface) int { return cmp.Compare(a.Index, b.Index) })
	return out, nil
}
