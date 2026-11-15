// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"net"
	"reflect"
	"strings"
	"testing"
)

// nas is an SNMP agent with the values of a NAS: strings, numbers and OIDs
// by OID.
func nas(values map[string]any) *fakeAgent {
	a := newAgent()
	a.values["1.3.6.1.2.1.1.1.0"] = berString([]byte("Linux nas 6.1.0"))
	a.values["1.3.6.1.2.1.1.5.0"] = berString([]byte("nas"))
	for k, v := range values {
		switch v := v.(type) {
		case string:
			if strings.HasPrefix(v, "oid:") {
				o, _ := parseOID(strings.TrimPrefix(v, "oid:"))
				a.values[k] = berOIDValue(o)
			} else {
				a.values[k] = berString([]byte(v))
			}
		case int:
			a.values[k] = berInt(int64(v))
		}
	}
	return a
}

// linuxBox is a Linux box with net-snmp: a pool of 8 TB in
// dskTable, its system disk in hrStorageTable, and two extend lines.
func linuxBox() *fakeAgent {
	const dsk, hr = "1.3.6.1.4.1.2021.9.1.", "1.3.6.1.2.1.25.2.3.1."
	snap, smart := extendIndex("snapraid"), extendIndex("smart")
	return nas(map[string]any{
		// 8e12 bytes in kB: 7812500000, so 1<<32 + 3517532704; 6e12 used,
		// 2e12 free.
		dsk + "2.1": "/srv/pool", dsk + "10.1": 3,
		dsk + "11.1": 3517532704, dsk + "12.1": 1,
		dsk + "13.1": 1953125000, dsk + "14.1": 0,
		dsk + "15.1": 1564407704, dsk + "16.1": 1,
		hr + "2.1": "oid:" + oidHrFixedDisk, hr + "3.1": "/", hr + "4.1": 4096, hr + "5.1": 1000000, hr + "6.1": 250000,
		hr + "2.2": "oid:1.3.6.1.2.1.25.2.1.2", hr + "3.2": "Physical memory", hr + "4.2": 1024, hr + "5.2": 100, hr + "6.2": 50,
		hr + "2.3": "oid:" + oidHrFixedDisk, hr + "3.3": "/run", hr + "4.3": 4096, hr + "5.3": 100, hr + "6.3": 1,
		hr + "2.4": "oid:" + oidHrFixedDisk, hr + "3.4": "/srv/pool", hr + "4.4": 4096, hr + "5.4": 100, hr + "6.4": 1,
		hr + "2.5": "oid:" + oidHrNetworkDisk, hr + "3.5": "/mnt/empty", hr + "4.5": 4096, hr + "5.5": 0, hr + "6.5": 0,
		oidExtendCommand + "." + snap: "/usr/local/bin/eyrie-nas-health", oidExtendCommand + "." + smart: "/usr/local/bin/eyrie-nas-health",
		oidExtendCommand + ".3.1.2.3": "not a name",
		oidExtendResult + "." + snap:  0, oidExtendLine1 + "." + snap: "SnapRAID: no errors, synced 3 hours ago",
		oidExtendResult + "." + smart: 1, oidExtendLine1 + "." + smart: "/dev/sdb: SMART says the disk is failing",
	})
}

func synology() *fakeAgent {
	const sys, disk, raid = "1.3.6.1.4.1.6574.1.", "1.3.6.1.4.1.6574.2.1.1.", "1.3.6.1.4.1.6574.3.1.1."
	return nas(map[string]any{
		sys + "1.0": 1, sys + "2.0": 41, sys + "3.0": 1, sys + "4.1.0": 1, sys + "4.2.0": 1,
		sys + "5.1.0": "DSX1", sys + "5.3.0": "DSM 7.0-00000",
		disk + "2.1": "Disk 1", disk + "5.1": 1, disk + "6.1": 36, disk + "13.1": 1,
		disk + "2.2": "Disk 2", disk + "5.2": 1, disk + "6.2": 52, disk + "13.2": 2,
		raid + "2.1": "Storage Pool 1", raid + "3.1": 1,
		raid + "2.2": "Volume 1", raid + "3.2": 13,
		raid + "2.3": "Volume 2", raid + "3.3": 2,
		"1.3.6.1.2.1.25.2.3.1.2.31": "oid:" + oidHrFixedDisk, "1.3.6.1.2.1.25.2.3.1.3.31": "/volume1",
		"1.3.6.1.2.1.25.2.3.1.4.31": 65536, "1.3.6.1.2.1.25.2.3.1.5.31": 100000000, "1.3.6.1.2.1.25.2.3.1.6.31": 40000000,
		// The same filesystem, mounted again for containers.
		"1.3.6.1.2.1.25.2.3.1.2.30": "oid:" + oidHrFixedDisk, "1.3.6.1.2.1.25.2.3.1.3.30": "/volume1/@appdata/shares/media",
		"1.3.6.1.2.1.25.2.3.1.4.30": 65536, "1.3.6.1.2.1.25.2.3.1.5.30": 100000000, "1.3.6.1.2.1.25.2.3.1.6.30": 40000000,
		"1.3.6.1.2.1.25.2.3.1.2.32": "oid:" + oidHrFixedDisk, "1.3.6.1.2.1.25.2.3.1.3.32": "/volume1/@docker",
		"1.3.6.1.2.1.25.2.3.1.4.32": 65536, "1.3.6.1.2.1.25.2.3.1.5.32": 100000000, "1.3.6.1.2.1.25.2.3.1.6.32": 40000000,
	})
}

func qnap() *fakeAgent {
	const vol, hd = "1.3.6.1.4.1.24681.1.3.17.1.", "1.3.6.1.4.1.24681.1.3.11.1."
	return nas(map[string]any{
		vol + "2.1": "DataVol1", vol + "6.1": "Ready",
		vol + "2.2": "DataVol2", vol + "6.2": "Rebuilding...",
		hd + "2.1": "HDD1", hd + "3.1": 38, hd + "4.1": 0, hd + "7.1": "GOOD",
		hd + "2.2": "HDD2", hd + "3.2": 0, hd + "4.2": -5, hd + "7.2": "--",
		hd + "2.3": "HDD3", hd + "3.3": 45, hd + "4.3": -9, hd + "7.3": "Abnormal",
		oidQnapFanStatus + ".1": 0, oidQnapFanStatus + ".2": -1, oidQnapPowerStatus + ".1": 0,
	})
}

func oldQnap() *fakeAgent {
	const vol, hd = "1.3.6.1.4.1.24681.1.2.17.1.", "1.3.6.1.4.1.24681.1.2.11.1."
	return nas(map[string]any{
		vol + "2.1": "Volume1", vol + "6.1": "Degraded",
		hd + "2.1": "HDD1", hd + "3.1": "40 C/104 F", hd + "4.1": 0, hd + "7.1": "GOOD",
		hd + "2.2": "HDD2", hd + "3.2": "--", hd + "4.2": 0, hd + "7.2": "GOOD",
		oidQnapPowerStatus + ".1": 0,
	})
}

func asustor(adm51 bool) *fakeAgent {
	const disk, vol = "1.3.6.1.4.1.44738.4.1.1.", "1.3.6.1.4.1.44738.5.1.1."
	v := map[string]any{
		vol + "2.1": "Volume 1", vol + "4.1": "Healthy",
		vol + "2.2": "Volume 2", vol + "4.2": "Recovering",
		disk + "2.1": "Disk1", disk + "5.1": "Good",
		disk + "2.2": "Disk2", disk + "5.2": "Abnormal",
	}
	if adm51 {
		v[disk+"6.1"], v[disk+"7.1"], v[disk+"6.2"], v[disk+"7.2"] = "Healthy", 40, "Risky", 44
	} else {
		v[disk+"6.1"], v[disk+"6.2"] = 39, 43
	}
	return nas(v)
}

func truenas() *fakeAgent {
	return nas(map[string]any{
		oidZpoolEntry + ".2.1": "tank", oidZpoolEntry + ".3.1": "ONLINE",
		oidZpoolEntry + ".2.2": "backup", oidZpoolEntry + ".3.2": "DEGRADED",
		oidHddTempEntry + ".2.1": "sda", oidHddTempEntry + ".3.1": 34000,
		oidHddTempEntry + ".2.2": "sdb", oidHddTempEntry + ".3.2": 37500,
	})
}

// healthy makes a fixture's device healthy, with values by OID.
func healthy(a *fakeAgent, values map[string]int) *fakeAgent {
	for k, v := range values {
		a.values[k] = berInt(int64(v))
	}
	return a
}

func TestSNMPStorage(t *testing.T) {
	const synoDisk, synoSys = "1.3.6.1.4.1.6574.2.1.1.", "1.3.6.1.4.1.6574.1."
	smartOK := map[string]int{oidExtendResult + "." + extendIndex("smart"): 0}
	goodSyno := func() *fakeAgent {
		return healthy(synology(), map[string]int{synoDisk + "13.2": 1, synoDisk + "6.2": 45})
	}
	for _, c := range []struct {
		name     string
		agent    func() *fakeAgent
		settings string
		ok       bool
		msg      string
	}{
		{"linux", linuxBox, ``, false, "/dev/sdb: SMART says the disk is failing (exit status 1)"},
		{"linux healthy", func() *fakeAgent { return healthy(linuxBox(), smartOK) }, ``, true,
			"/srv/pool: 75% used, 2.0 TB free of 8.0 TB; 2 commands OK"},
		{"full", func() *fakeAgent { return healthy(linuxBox(), smartOK) }, `,"max_percent":70,"max_inode_percent":2`, false,
			"/srv/pool 75% used, 3% of inodes used"},
		{"little free", func() *fakeAgent { return healthy(linuxBox(), smartOK) }, `,"min_free_gb":3000`, false, "/ 3.1 GB free, /srv/pool 2.0 TB free"},
		{"chosen volumes", func() *fakeAgent { return healthy(linuxBox(), smartOK) }, `,"volumes":["/"," /nope",""],"max_percent":20`, false,
			"no volume /nope, / 25% used"},
		{"chosen volume", func() *fakeAgent { return healthy(linuxBox(), smartOK) }, `,"volumes":["/"]`, true,
			"/: 25% used, 3.1 GB free of 4.1 GB; 2 commands OK"},
		{"ignored volume", func() *fakeAgent { return healthy(linuxBox(), smartOK) }, `,"ignore_volumes":["/srv/pool"," /srv/pool"],"max_percent":70`, true,
			"/: 25% used, 3.1 GB free of 4.1 GB; 2 commands OK"},
		{"ignored and chosen", func() *fakeAgent { return healthy(linuxBox(), smartOK) }, `,"volumes":["/nope","/"],"ignore_volumes":["/nope","/"]`, true,
			"2 commands OK"},

		{"synology", synology, ``, false, "Disk 2 normal, health warning, Disk 2 at 52 °C"},
		{"synology healthy", goodSyno, ``, true,
			"/volume1: 40% used, 3.9 TB free of 6.6 TB; 3 volumes healthy; 2 disks healthy; hottest disk 45 °C"},
		{"repairing down", goodSyno, `,"repairing_down":true`, false, "Volume 2 repairing"},
		{"synology too hot", goodSyno, `,"max_celsius":40`, false, "Disk 2 at 45 °C"},
		{"synology power", func() *fakeAgent { return healthy(goodSyno(), map[string]int{synoSys + "3.0": 2}) }, ``, false,
			"System normal, power failed, system fan normal, CPU fan normal, 41 °C"},

		{"qnap", qnap, ``, false, "HDD3 read/write error, SMART abnormal, 1 of 2 fans failed, power supplies normal"},
		{"old qnap", oldQnap, ``, false, "Volume1 degraded"},
		{"asustor", func() *fakeAgent { return asustor(true) }, ``, false, "Disk2 abnormal, SMART risky"},
		{"older asustor", func() *fakeAgent { return asustor(false) }, ``, false, "Disk2 abnormal"},
		{"truenas", truenas, `,"max_celsius":35`, false, "backup degraded, sdb at 37 °C"},
		{"nothing", func() *fakeAgent { return nas(nil) }, ``, false, "The device reports no storage over SNMP"},
	} {
		env := testEnv()
		env.Dial = pipeDial(c.agent().serve, nil)
		r := run(t, env, "snmp-storage", `{"host":"nas.example.com"`+c.settings+`}`)
		if r.OK != c.ok || r.Message != c.msg || (c.name != "nothing" && r.Storage == nil) {
			t.Errorf("%s: %+v", c.name, r)
		}
	}
}

func TestSNMPStorageErrors(t *testing.T) {
	env := testEnv()
	a := linuxBox()
	a.errStatus = 5
	env.Dial = pipeDial(a.serve, nil)
	if r := run(t, env, "snmp-storage", `{"host":"nas.example.com"}`); r.OK || r.Message != "The agent answered genErr" || r.Storage != nil {
		t.Errorf("error status: %+v", r)
	}
	env.Dial = func(context.Context, string, string) (net.Conn, error) { return nil, context.DeadlineExceeded }
	if r := run(t, env, "snmp-storage", `{"host":"nas.example.com"}`); r.OK || r.Message != "Timed out" {
		t.Errorf("dial: %+v", r)
	}
	for _, s := range []string{
		`{}`,
		`{"host":"nas.example.com","max_percent":101}`,
		`{"host":"nas.example.com","max_inode_percent":-1}`,
		`{"host":"nas.example.com","min_free_gb":-1}`,
		`{"host":"nas.example.com","max_celsius":200}`,
		`{"host":"nas.example.com","watch":"capacity"}`,
	} {
		if _, _, err := Parse(testEnv(), "snmp-storage", []byte(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
}

func TestSNMPStorageDescribe(t *testing.T) {
	s := SNMPStorage{SNMPAgent: SNMPAgent{Host: "nas.example.com", Port: 161, Version: "2c"}, MaxPercent: 90, MaxCelsius: 50}
	if f := s.describe(); len(f) != 5 || f[2].Value != "all" {
		t.Errorf("defaults: %v", f)
	}
	s.Volumes, s.IgnoreVolumes, s.MinFreeGB, s.MaxInodePercent = []string{"/volume1"}, []string{"/volume2"}, 100, 5
	if f := s.describe(); len(f) != 8 || f[2].Value != "/volume1" || f[5].Value != "/volume2" {
		t.Errorf("all: %v", f)
	}
}

func TestStorageText(t *testing.T) {
	for b, want := range map[int64]string{999: "999 B", 1500: "1.5 kB", 2e12: "2.0 TB", 3e18: "3000.0 PB"} {
		if got := sizeText(b); got != want {
			t.Errorf("%d: %s", b, got)
		}
	}
	if got := bigMul(1<<40, 1<<40); got != 1<<63-1 {
		t.Errorf("overflow: %d", got)
	}
	if got := clipLine(strings.Repeat("é", 150)); len(got) != 200 {
		t.Errorf("clip: %d", len(got))
	}
	for _, idx := range []string{"x", "3.1.2", "1.300"} {
		if extendNameOf(idx) != "" {
			t.Errorf("%s read as a name", idx)
		}
	}
	if extendNameOf(extendIndex("smart")) != "smart" {
		t.Error("round trip")
	}
	many := make([]string, 12)
	for i := range many {
		many[i] = "x"
	}
	if got := listText(many); !strings.HasSuffix(got, " and 2 more") {
		t.Errorf("list: %s", got)
	}
	if plural(1, "disk", "disks") != "1 disk" || plural(3, "disk", "disks") != "3 disks" {
		t.Error("plural")
	}
	for _, c := range []struct {
		c    StorageCommand
		want string
	}{{StorageCommand{Name: "a"}, "a exited with 0"}, {StorageCommand{Name: "a", Status: 2}, "a exited with 2"}} {
		if got := c.c.message(); got != c.want {
			t.Errorf("%+v: %s", c.c, got)
		}
	}
}

func TestDiscoverStorage(t *testing.T) {
	ctx := context.Background()
	env := testEnv()
	env.Dial = pipeDial(synology().serve, nil)
	d, err := DiscoverStorage(ctx, env, []byte(`{"host":"nas.example.com","oid":"ignored"}`))
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "nas" || d.Description != "Synology DSX1, DSM 7.0-00000" || d.System == nil || !d.System.OK ||
		len(d.Volumes) != 1 || len(d.Pools) != 3 || len(d.Disks) != 2 || len(d.Temps) != 2 {
		t.Errorf("device: %+v", d)
	}
	if ok, msg := d.Judge(); ok || msg != "Disk 2 normal, health warning, Disk 2 at 52 °C" {
		t.Errorf("judged %v %s", ok, msg)
	}

}

func TestDiscoverLinuxStorage(t *testing.T) {
	ctx := context.Background()
	env := testEnv()
	env.Dial = pipeDial(linuxBox().serve, nil)
	d, err := DiscoverStorage(ctx, env, []byte(`{"host":"nas.example.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, v := range d.Volumes {
		names = append(names, v.Name)
	}
	for _, c := range d.Commands {
		names = append(names, c.Name)
	}
	if !reflect.DeepEqual(names, []string{"/", "/srv/pool", "smart", "snapraid"}) || d.System != nil {
		t.Errorf("linux: %q %+v", names, d.System)
	}

	a := linuxBox()
	a.errStatus = 5
	env.Dial = pipeDial(a.serve, nil)
	if _, err := DiscoverStorage(ctx, env, []byte(`{"host":"nas.example.com"}`)); err == nil {
		t.Error("error status")
	}
	for _, s := range []string{`[`, `{"host":""}`} {
		if _, err := DiscoverStorage(ctx, env, []byte(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
	env.Dial = func(context.Context, string, string) (net.Conn, error) { return nil, context.DeadlineExceeded }
	if _, err := DiscoverStorage(ctx, env, []byte(`{"host":"nas.example.com"}`)); err == nil || err.Error() != "Timed out" {
		t.Errorf("dial: %v", err)
	}
}
