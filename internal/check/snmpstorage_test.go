// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"encoding/json"
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

// linuxBox is a Debian box with net-snmp: a mergerfs pool of 8 TB in
// dskTable, its system disk in hrStorageTable, and two extend lines.
func linuxBox() *fakeAgent {
	const dsk, hr = "1.3.6.1.4.1.2021.9.1.", "1.3.6.1.2.1.25.2.3.1."
	snap, smart := extendIndex("snapraid"), extendIndex("smart")
	return nas(map[string]any{
		// 8e12 bytes in kB: 7812500000, so 1<<32 + 3517532704; 6e12 used,
		// 2e12 free.
		dsk + "2.1": "/mnt/storage", dsk + "10.1": 3,
		dsk + "11.1": 3517532704, dsk + "12.1": 1,
		dsk + "13.1": 1953125000, dsk + "14.1": 0,
		dsk + "15.1": 1564407704, dsk + "16.1": 1,
		hr + "2.1": "oid:" + oidHrFixedDisk, hr + "3.1": "/", hr + "4.1": 4096, hr + "5.1": 1000000, hr + "6.1": 250000,
		hr + "2.2": "oid:1.3.6.1.2.1.25.2.1.2", hr + "3.2": "Physical memory", hr + "4.2": 1024, hr + "5.2": 100, hr + "6.2": 50,
		hr + "2.3": "oid:" + oidHrFixedDisk, hr + "3.3": "/run", hr + "4.3": 4096, hr + "5.3": 100, hr + "6.3": 1,
		hr + "2.4": "oid:" + oidHrFixedDisk, hr + "3.4": "/mnt/storage", hr + "4.4": 4096, hr + "5.4": 100, hr + "6.4": 1,
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

func TestSNMPStorage(t *testing.T) {
	for _, c := range []struct {
		name     string
		agent    func() *fakeAgent
		settings string
		ok       bool
		msg      string
	}{
		{"pool", linuxBox, `"volume":"/mnt/storage"`, true, "/mnt/storage: 75% used, 2.0 TB free of 8.0 TB"},
		{"pool full", linuxBox, `"volume":"/mnt/storage","max_percent":70,"min_free_gb":3000`, false,
			"/mnt/storage: 75% used, 2.0 TB free of 8.0 TB: more than 70% used and less than 3000 GB free"},
		{"inodes", linuxBox, `"watch":"capacity","volume":"/mnt/storage","max_inode_percent":2`, false,
			"/mnt/storage: 75% used, 2.0 TB free of 8.0 TB, 3% of inodes used: more than 2% of inodes used"},
		{"root", linuxBox, `"volume":"/"`, true, "/: 25% used, 3.1 GB free of 4.1 GB"},
		{"command", linuxBox, `"watch":"command","command":"snapraid"`, true, "SnapRAID: no errors, synced 3 hours ago"},
		{"command fails", linuxBox, `"watch":"command","command":"smart"`, false, "/dev/sdb: SMART says the disk is failing (exit status 1)"},
		{"no health", linuxBox, `"watch":"volume-health"`, false, "The device reports no volume health over SNMP: " +
			"Synology DSM, QNAP QTS, Asustor ADM and TrueNAS do; on others, watch a command that snmpd runs"},
		{"no system", linuxBox, `"watch":"system-health"`, false, "The device reports no system health over SNMP: " +
			"Synology DSM, QNAP QTS, Asustor ADM and TrueNAS do; on others, watch a command that snmpd runs"},
		{"no disks", linuxBox, `"watch":"disk-health"`, false, "The device reports no disk health over SNMP: " +
			"Synology DSM, QNAP QTS, Asustor ADM and TrueNAS do; on others, watch a command that snmpd runs"},
		{"no temperatures", linuxBox, `"watch":"disk-temperature"`, false, "The device reports no disk temperatures over SNMP: " +
			"Synology DSM, QNAP QTS, Asustor ADM and TrueNAS do; on others, watch a command that snmpd runs"},

		{"synology space", synology, `"volume":"/volume1"`, true, "/volume1: 40% used, 3.9 TB free of 6.6 TB"},
		{"no volume", synology, `"volume":"/volume9"`, false, `The device has no volume "/volume9"; it has /volume1`},
		{"synology volumes", synology, `"watch":"volume-health"`, true,
			"Storage Pool 1 normal, Volume 1 scrubbing, Volume 2 repairing"},
		{"repairing down", synology, `"watch":"volume-health","volume":"Volume 2","repairing_down":true`, false, "Volume 2 repairing"},
		{"synology volume", synology, `"watch":"volume-health","volume":"Volume 1"`, true, "Volume 1 scrubbing"},
		{"synology disks", synology, `"watch":"disk-health"`, false, "Disk 2 normal, health warning"},
		{"synology disk", synology, `"watch":"disk-health","disk":"Disk 1"`, true, "Disk 1 normal"},
		{"unknown disk", synology, `"watch":"disk-health","disk":"Disk 9"`, false, `The device has no disk "Disk 9"; it has Disk 1, Disk 2`},
		{"synology hot", synology, `"watch":"disk-temperature"`, false, "Above 50 °C: Disk 2 52 °C"},
		{"synology cool", synology, `"watch":"disk-temperature","max_celsius":55`, true, "Hottest of 2 disks: Disk 2 52 °C"},
		{"one disk", synology, `"watch":"disk-temperature","disk":"Disk 1"`, true, "Disk 1 36 °C"},
		{"no such disk", synology, `"watch":"disk-temperature","disk":"Disk 9"`, false, `The device has no disk "Disk 9"; it has Disk 1, Disk 2`},
		{"synology system", synology, `"watch":"system-health"`, true,
			"System normal, power normal, system fan normal, CPU fan normal, 41 °C"},

		{"qnap volumes", qnap, `"watch":"volume-health"`, true, "DataVol1 ready, DataVol2 rebuilding..."},
		{"qnap disks", qnap, `"watch":"disk-health"`, false, "HDD3 read/write error, SMART abnormal"},
		{"qnap temperature", qnap, `"watch":"disk-temperature"`, true, "Hottest of 2 disks: HDD3 45 °C"},
		{"qnap system", qnap, `"watch":"system-health"`, false, "1 of 2 fans failed, power supplies normal"},
		{"old qnap volumes", oldQnap, `"watch":"volume-health"`, false, "Volume1 degraded"},
		{"old qnap disks", oldQnap, `"watch":"disk-health"`, true, "HDD1 ready, HDD2 ready"},
		{"old qnap temperature", oldQnap, `"watch":"disk-temperature"`, true, "HDD1 40 °C"},

		{"asustor volumes", func() *fakeAgent { return asustor(true) }, `"watch":"volume-health"`, true, "Volume 1 healthy, Volume 2 recovering"},
		{"asustor disks", func() *fakeAgent { return asustor(true) }, `"watch":"disk-health"`, false, "Disk2 abnormal, SMART risky"},
		{"asustor temperature", func() *fakeAgent { return asustor(true) }, `"watch":"disk-temperature"`, true, "Hottest of 2 disks: Disk2 44 °C"},
		{"older asustor", func() *fakeAgent { return asustor(false) }, `"watch":"disk-temperature"`, true, "Hottest of 2 disks: Disk2 43 °C"},
		{"older asustor disks", func() *fakeAgent { return asustor(false) }, `"watch":"disk-health","disk":"Disk1"`, true, "Disk1 good"},

		{"truenas pools", truenas, `"watch":"volume-health"`, false, "backup degraded"},
		{"truenas pool", truenas, `"watch":"volume-health","volume":"tank"`, true, "tank online"},
		{"truenas temperature", truenas, `"watch":"disk-temperature"`, true, "Hottest of 2 disks: sdb 37 °C"},
	} {
		env := testEnv()
		env.Dial = pipeDial(c.agent().serve, nil)
		r := run(t, env, "snmp-storage", `{"host":"nas.example.com",`+c.settings+`}`)
		if r.OK != c.ok || r.Message != c.msg {
			t.Errorf("%s: %+v", c.name, r)
		}
	}
}

func TestSNMPStorageErrors(t *testing.T) {
	env := testEnv()
	a := linuxBox()
	env.Dial = pipeDial(a.serve, nil)
	if r := run(t, env, "snmp-storage", `{"host":"nas.example.com","watch":"command","command":"backup"}`); r.OK ||
		r.Message != "Snmpd has no extend line named backup: add one to snmpd.conf" {
		t.Errorf("no extend: %+v", r)
	}
	a = nas(nil)
	env.Dial = pipeDial(a.serve, nil)
	if r := run(t, env, "snmp-storage", `{"host":"nas.example.com","volume":"/"}`); r.OK || r.Message != "The device reports no volumes" {
		t.Errorf("no volumes: %+v", r)
	}
	a = linuxBox()
	a.errStatus = 5
	env.Dial = pipeDial(a.serve, nil)
	for _, w := range []string{`"volume":"/"`, `"watch":"volume-health"`, `"watch":"disk-health"`, `"watch":"disk-temperature"`} {
		if r := run(t, env, "snmp-storage", `{"host":"nas.example.com",`+w+`}`); r.OK || r.Message != "The agent answered genErr" {
			t.Errorf("%s: %+v", w, r)
		}
	}
	env.Dial = func(context.Context, string, string) (net.Conn, error) { return nil, context.DeadlineExceeded }
	if r := run(t, env, "snmp-storage", `{"host":"nas.example.com","volume":"/"}`); r.OK || r.Message != "Timed out" {
		t.Errorf("dial: %+v", r)
	}
	for _, s := range []string{
		`{"volume":"/"}`,
		`{"host":"nas.example.com","watch":"smart"}`,
		`{"host":"nas.example.com"}`,
		`{"host":"nas.example.com","watch":"command","command":"two words"}`,
		`{"host":"nas.example.com","volume":"/","max_percent":101}`,
		`{"host":"nas.example.com","volume":"/","max_inode_percent":-1}`,
		`{"host":"nas.example.com","volume":"/","min_free_gb":-1}`,
		`{"host":"nas.example.com","watch":"disk-temperature","max_celsius":200}`,
		`{"host":"nas.example.com","volume":"/","oid":"1.3.6"}`,
	} {
		if _, _, err := Parse(testEnv(), "snmp-storage", []byte(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
}

func TestSNMPStorageDescribe(t *testing.T) {
	for _, c := range []struct {
		s    SNMPStorage
		want int
	}{
		{SNMPStorage{Watch: WatchCapacity, Volume: "/", MaxPercent: 90, MinFreeGB: 10, MaxInodePercent: 5}, 7},
		{SNMPStorage{Watch: WatchVolumeHealth}, 4},
		{SNMPStorage{Watch: WatchDiskHealth, Disk: "Disk 1"}, 4},
		{SNMPStorage{Watch: WatchDiskTemperature, MaxCelsius: 50}, 5},
		{SNMPStorage{Watch: WatchSystemHealth}, 3},
		{SNMPStorage{Watch: WatchCommand, Command: "snapraid"}, 4},
	} {
		c.s.Host, c.s.Port, c.s.Version = "nas.example.com", 161, "2c"
		if f := c.s.describe(); len(f) != c.want {
			t.Errorf("%s: %v", c.s.Watch, f)
		}
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
	vols := make([]StorageVolume, 11)
	if err := missing("volume", "/x", vols, func(StorageVolume) string { return "v" }); !strings.HasSuffix(err.Error(), ", …") {
		t.Errorf("missing: %v", err)
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
	if d.Name != "nas" || d.Description != "Synology DSX1, DSM 7.0-00000" || d.System == nil || !d.System.OK {
		t.Errorf("device: %+v", d)
	}
	var got []string
	for _, s := range d.Suggestions() {
		b, _ := json.Marshal(s.Settings)
		got = append(got, s.Name+"|"+s.Detail+"|"+map[bool]string{true: "up", false: "down"}[s.Up]+"|"+string(b))
	}
	want := []string{
		`/volume1 space|/volume1: 40% used, 3.9 TB free of 6.6 TB|up|{"volume":"/volume1","watch":"capacity"}`,
		`Storage Pool 1 health|Normal|up|{"volume":"Storage Pool 1","watch":"volume-health"}`,
		`Volume 1 health|Scrubbing|up|{"volume":"Volume 1","watch":"volume-health"}`,
		`Volume 2 health|Repairing|up|{"volume":"Volume 2","watch":"volume-health"}`,
		`disk health|Disk 2 normal, health warning|down|{"disk":"","watch":"disk-health"}`,
		`disk temperature|Hottest: Disk 2 52 °C|down|{"disk":"","watch":"disk-temperature"}`,
		`system health|System normal, power normal, system fan normal, CPU fan normal, 41 °C|up|{"watch":"system-health"}`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("suggestions:\n%s", strings.Join(got, "\n"))
	}

	env.Dial = pipeDial(linuxBox().serve, nil)
	if d, err = DiscoverStorage(ctx, env, []byte(`{"host":"nas.example.com"}`)); err != nil {
		t.Fatal(err)
	}
	got = nil
	for _, s := range d.Suggestions() {
		got = append(got, s.Name+"|"+s.Detail)
	}
	want = []string{"/mnt/storage space|/mnt/storage: 75% used, 2.0 TB free of 8.0 TB", "/ space|/: 25% used, 3.1 GB free of 4.1 GB",
		"smart|/dev/sdb: SMART says the disk is failing (exit status 1)", "snapraid|SnapRAID: no errors, synced 3 hours ago"}
	if !reflect.DeepEqual(got, want) || d.System != nil {
		t.Errorf("linux: %q %+v", got, d.System)
	}

	env.Dial = pipeDial(qnap().serve, nil)
	if d, err = DiscoverStorage(ctx, env, []byte(`{"host":"nas.example.com"}`)); err != nil || len(d.Disks) != 2 || d.Suggestions()[2].Detail != "HDD3 read/write error, SMART abnormal" {
		t.Errorf("qnap: %+v %v", d, err)
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
