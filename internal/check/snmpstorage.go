// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// SNMP storage: a NAS's volumes, disks and health, read over SNMP. Space
// comes from the standard tables (HOST-RESOURCES-MIB's hrStorageTable and
// net-snmp's dskTable), which every NAS has; health from Synology's, QNAP's
// and TrueNAS's own MIBs, whichever the device answers; and anything else,
// such as SnapRAID or SMART on a Linux box, from a command snmpd runs
// (NET-SNMP-EXTEND-MIB).

// SNMPStorage is the settings of the snmp-storage type.
type SNMPStorage struct {
	SNMPAgent
	// Watch is what is checked: capacity (the default), volume-health,
	// disk-health, disk-temperature, system-health or command.
	Watch string `json:"watch"`
	// Volume is a volume as the device names it: a mount point such as
	// /volume1 for capacity, the volume or pool's name for its health
	// ("": all of them).
	Volume string `json:"volume"`
	// Disk is a disk as the device names it; "": all of them.
	Disk string `json:"disk"`
	// Capacity: down above MaxPercent of the space used (90 by default),
	// below MinFreeGB gigabytes free or above MaxInodePercent of the
	// inodes used (0: no limit).
	MaxPercent      int `json:"max_percent"`
	MinFreeGB       int `json:"min_free_gb"`
	MaxInodePercent int `json:"max_inode_percent"`
	// RepairingDown makes a volume being repaired or rebuilt down; it is
	// up by default, as its failure was already down.
	RepairingDown bool `json:"repairing_down"`
	// MaxCelsius is the hottest a disk may be: 50 by default.
	MaxCelsius int `json:"max_celsius"`
	// Command is the name of an extend line in snmpd.conf: up when the
	// command it runs exits with 0.
	Command string `json:"command"`
}

// The watches of snmp-storage.
const (
	WatchCapacity        = "capacity"
	WatchVolumeHealth    = "volume-health"
	WatchDiskHealth      = "disk-health"
	WatchDiskTemperature = "disk-temperature"
	WatchSystemHealth    = "system-health"
	WatchCommand         = "command"
)

var (
	storageWatches = []string{WatchCapacity, WatchVolumeHealth, WatchDiskHealth, WatchDiskTemperature, WatchSystemHealth, WatchCommand}
	extendName     = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
)

func (s *SNMPStorage) normalize() error {
	if err := s.normalizeAgent(); err != nil {
		return err
	}
	if s.Watch == "" {
		s.Watch = WatchCapacity
	}
	if !slices.Contains(storageWatches, s.Watch) {
		return fmt.Errorf("what to watch must be one of %s", strings.Join(storageWatches, ", "))
	}
	s.Volume, s.Disk, s.Command = strings.TrimSpace(s.Volume), strings.TrimSpace(s.Disk), strings.TrimSpace(s.Command)
	if s.MaxPercent == 0 {
		s.MaxPercent = 90
	}
	if s.MaxCelsius == 0 {
		s.MaxCelsius = 50
	}
	switch {
	case s.Watch == WatchCapacity && s.Volume == "":
		return errors.New("the volume is required, such as /volume1")
	case s.Watch == WatchCommand && !extendName.MatchString(s.Command):
		return errors.New("the command is the name of an extend line in snmpd.conf: letters, digits, dots, dashes and underscores")
	case s.MaxPercent < 1 || s.MaxPercent > 100 || s.MaxInodePercent < 0 || s.MaxInodePercent > 100:
		return errors.New("a percentage is from 1 to 100")
	case s.MinFreeGB < 0:
		return errors.New("the free space cannot be negative")
	case s.MaxCelsius < 1 || s.MaxCelsius > 150:
		return errors.New("the highest temperature is from 1 to 150 °C")
	}
	return nil
}

func (s *SNMPStorage) checker(env *Env) Checker { return &storageChecker{s: s, env: env} }

func (s *SNMPStorage) describe() []Fact {
	f := append(s.facts(), Fact{"Watch", s.Watch})
	switch s.Watch {
	case WatchCapacity:
		f = append(f, Fact{"Volume", s.Volume}, Fact{"Highest use", strconv.Itoa(s.MaxPercent) + "%"})
		if s.MinFreeGB > 0 {
			f = append(f, Fact{"Least free space", strconv.Itoa(s.MinFreeGB) + " GB"})
		}
		if s.MaxInodePercent > 0 {
			f = append(f, Fact{"Highest inode use", strconv.Itoa(s.MaxInodePercent) + "%"})
		}
	case WatchVolumeHealth:
		f = append(f, Fact{"Volume", cmp.Or(s.Volume, "all")})
	case WatchDiskHealth:
		f = append(f, Fact{"Disk", cmp.Or(s.Disk, "all")})
	case WatchDiskTemperature:
		f = append(f, Fact{"Disk", cmp.Or(s.Disk, "all")}, Fact{"Highest temperature", strconv.Itoa(s.MaxCelsius) + " °C"})
	case WatchCommand:
		f = append(f, Fact{"Command", s.Command})
	}
	return f
}

type storageChecker struct {
	s   *SNMPStorage
	env *Env
}

func (c *storageChecker) Check(ctx context.Context) Result {
	w, err := dialWalker(ctx, c.env, &c.s.SNMPAgent)
	if err != nil {
		return fail("%s", dialError(err))
	}
	defer w.conn.Close()
	start := time.Now()
	ok, msg, err := c.check(w)
	if err != nil {
		return failErr(err)
	}
	return Result{OK: ok, Message: msg, Latency: time.Since(start)}
}

func (c *storageChecker) check(w *walker) (bool, string, error) {
	s := c.s
	switch s.Watch {
	case WatchVolumeHealth:
		return c.states(w, "volume", s.Volume, volumeStates)
	case WatchDiskHealth:
		return c.states(w, "disk", s.Disk, diskStates)
	case WatchDiskTemperature:
		return c.temperature(w)
	case WatchSystemHealth:
		st, err := systemState(w)
		if err != nil {
			return false, "", err
		}
		return st.OK, st.State, nil
	case WatchCommand:
		cmd, err := extendResult(w, s.Command)
		if err != nil {
			return false, "", err
		}
		return cmd.Status == 0, cmd.message(), nil
	}
	return c.capacity(w)
}

func dialWalker(ctx context.Context, env *Env, a *SNMPAgent) (*walker, error) {
	conn, err := env.Dial(ctx, "udp", net.JoinHostPort(a.Host, strconv.Itoa(a.Port)))
	if err != nil {
		return nil, err
	}
	conn.SetDeadline(deadline(ctx)) //nolint:errcheck,gosec // a failure shows as a timeout
	return &walker{c: &snmpSession{s: a, env: env}, ctx: ctx, conn: conn}, nil
}

// row is a table's row: its index, and its values by column.
type row struct {
	index string
	v     map[int]string
}

// table reads the columns of a table's entry, by row; nothing when the
// device has no such table (its first column is empty).
func (w *walker) table(entry string, cols ...int) ([]row, error) {
	byIndex := map[string]row{}
	var out []row
	for i, col := range cols {
		vals, err := w.walk(entry + "." + strconv.Itoa(col))
		if err != nil {
			return nil, err
		}
		if i == 0 && len(vals) == 0 {
			return nil, nil
		}
		for idx, v := range vals {
			r, ok := byIndex[idx]
			if !ok {
				if i > 0 {
					continue
				}
				r = row{index: idx, v: map[int]string{}}
				byIndex[idx] = r
				out = append(out, r)
			}
			r.v[col] = v
		}
	}
	slices.SortFunc(out, func(a, b row) int {
		x, _ := parseOID("1." + a.index)
		y, _ := parseOID("1." + b.index)
		return slices.Compare(x, y)
	})
	return out, nil
}

// num reads a whole number; 0 when it is not one.
func num(s string) int64 {
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}

// Space.

// Tables of space.
const (
	oidHrStorageEntry = "1.3.6.1.2.1.25.2.3.1"
	oidDskEntry       = "1.3.6.1.4.1.2021.9.1"
	// hrStorageTypes that are disks: fixed and network.
	oidHrFixedDisk   = "1.3.6.1.2.1.25.2.1.4"
	oidHrNetworkDisk = "1.3.6.1.2.1.25.2.1.10"
)

// StorageVolume is a volume's space.
type StorageVolume struct {
	Name  string `json:"name"`
	Total int64  `json:"total"` // bytes
	Free  int64  `json:"free"`  // bytes
	// Percent is how much is used, as df counts it.
	Percent float64 `json:"percent"`
	// InodePercent is how many inodes are used; -1: not known.
	InodePercent int `json:"inode_percent"`
}

func (v StorageVolume) String() string {
	return fmt.Sprintf("%s: %.0f%% used, %s free of %s", v.Name, v.Percent, sizeText(v.Free), sizeText(v.Total))
}

// volumes reads net-snmp's dskTable, which has 64-bit sizes and inodes,
// then the hrStorageTable's disks that dskTable does not have.
func volumes(w *walker) ([]StorageVolume, error) {
	var out []StorageVolume
	dsk, err := w.table(oidDskEntry, 2, 10, 11, 12, 13, 14, 15, 16)
	if err != nil {
		return nil, err
	}
	for _, r := range dsk {
		kb := func(low, high int) int64 { return (num(r.v[high])<<32 | num(r.v[low])) * 1024 }
		total, avail, used := kb(11, 12), kb(13, 14), kb(15, 16)
		v := StorageVolume{Name: r.v[2], Total: total, Free: avail, InodePercent: -1}
		if used+avail > 0 {
			v.Percent = float64(used) * 100 / float64(used+avail)
		}
		if p, ok := r.v[10]; ok {
			v.InodePercent = int(num(p))
		}
		out = append(out, v)
	}
	hr, err := w.table(oidHrStorageEntry, 3, 2, 4, 5, 6)
	if err != nil {
		return nil, err
	}
	for _, r := range hr {
		if r.v[2] != oidHrFixedDisk && r.v[2] != oidHrNetworkDisk {
			continue
		}
		if slices.ContainsFunc(out, func(v StorageVolume) bool { return v.Name == r.v[3] }) {
			continue
		}
		// Integer32s, so multiplied as big numbers.
		unit := num(r.v[4])
		total, used := bigMul(num(r.v[5]), unit), bigMul(num(r.v[6]), unit)
		if total <= 0 {
			continue
		}
		out = append(out, StorageVolume{Name: r.v[3], Total: total, Free: total - used, Percent: float64(used) * 100 / float64(total),
			InodePercent: -1})
	}
	return out, nil
}

func (c *storageChecker) capacity(w *walker) (bool, string, error) {
	s := c.s
	vols, err := volumes(w)
	if err != nil {
		return false, "", err
	}
	i := slices.IndexFunc(vols, func(v StorageVolume) bool { return v.Name == s.Volume })
	if i < 0 {
		return false, "", missing("volume", s.Volume, vols, func(v StorageVolume) string { return v.Name })
	}
	v := vols[i]
	msg := v.String()
	if v.InodePercent >= 0 && s.MaxInodePercent > 0 {
		msg += fmt.Sprintf(", %d%% of inodes used", v.InodePercent)
	}
	var over []string
	if v.Percent > float64(s.MaxPercent) {
		over = append(over, fmt.Sprintf("more than %d%% used", s.MaxPercent))
	}
	if s.MinFreeGB > 0 && v.Free < int64(s.MinFreeGB)*1e9 {
		over = append(over, fmt.Sprintf("less than %d GB free", s.MinFreeGB))
	}
	if s.MaxInodePercent > 0 && v.InodePercent > s.MaxInodePercent {
		over = append(over, fmt.Sprintf("more than %d%% of inodes used", s.MaxInodePercent))
	}
	if len(over) > 0 {
		return false, msg + ": " + strings.Join(over, " and "), nil
	}
	return true, msg, nil
}

// sizeText writes bytes in decimal units, as disks are sold.
func sizeText(b int64) string {
	units := []string{"B", "kB", "MB", "GB", "TB", "PB"}
	f, i := float64(b), 0
	for f >= 1000 && i < len(units)-1 {
		f /= 1000
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d B", b)
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

// missing says that a device has no volume or disk of a name, and which it
// has.
func missing[T any](what, name string, have []T, nameOf func(T) string) error {
	if len(have) == 0 {
		return fmt.Errorf("the device reports no %ss", what)
	}
	var names []string
	for _, h := range have[:min(len(have), 10)] {
		names = append(names, nameOf(h))
	}
	if len(have) > 10 {
		names = append(names, "…")
	}
	return fmt.Errorf("the device has no %s %q; it has %s", what, name, strings.Join(names, ", "))
}

// Health.

// StorageState is how a volume, a disk or the system is.
type StorageState struct {
	Name  string `json:"name"`
	State string `json:"state"`
	OK    bool   `json:"ok"`
	// Repairing: being repaired or rebuilt, which is up unless a monitor
	// says otherwise.
	Repairing bool `json:"repairing,omitempty"`
}

// Synology's MIBs.
const (
	oidSynoSystem    = "1.3.6.1.4.1.6574.1"
	oidSynoModel     = "1.3.6.1.4.1.6574.1.5.1.0"
	oidSynoVersion   = "1.3.6.1.4.1.6574.1.5.3.0"
	oidSynoDiskEntry = "1.3.6.1.4.1.6574.2.1.1"
	oidSynoRaidEntry = "1.3.6.1.4.1.6574.3.1.1"
)

// QNAP's NAS-MIB: the tables of QTS 4 and later (systemInfoEx), then the
// older ones (systemInfo), whose columns are the same but give text.
const (
	oidQnapEx          = "1.3.6.1.4.1.24681.1.3"
	oidQnapOld         = "1.3.6.1.4.1.24681.1.2"
	oidQnapFanStatus   = "1.3.6.1.4.1.24681.1.4.1.1.1.1.2.2.1.4"
	oidQnapPowerStatus = "1.3.6.1.4.1.24681.1.4.1.1.1.1.3.2.1.4"
)

// Asustor's MIBs (ADM 4 and later). ADM 5.1 put diskSmartStatus where the
// temperature was, which moved one column on.
const (
	oidAsusDiskEntry   = "1.3.6.1.4.1.44738.4.1.1"
	oidAsusVolumeEntry = "1.3.6.1.4.1.44738.5.1.1"
)

// TrueNAS's MIB.
const (
	oidZpoolEntry   = "1.3.6.1.4.1.50536.1.1.1.1"
	oidHddTempEntry = "1.3.6.1.4.1.50536.3.1"
)

// synoRaid names the states of SYNOLOGY-RAID-MIB's raidStatus.
var synoRaid = map[int64]string{1: "normal", 2: "repairing", 3: "migrating", 4: "expanding", 5: "deleting", 6: "creating",
	7: "syncing", 8: "checking parity", 9: "assembling", 10: "cancelling", 11: "degraded", 12: "crashed", 13: "scrubbing",
	14: "deploying", 15: "undeploying", 16: "mounting its cache", 17: "unmounting its cache", 18: "expanding (unfinished SHR)",
	19: "converting from SHR to a storage pool", 20: "migrating from SHR-1 to SHR-2", 21: "in an unknown state"}

// synoDisk and synoHealth name SYNOLOGY-DISK-MIB's diskStatus and
// diskHealthStatus.
var (
	synoDisk   = map[int64]string{1: "normal", 2: "initialized", 3: "not initialized", 4: "system partition failed", 5: "crashed"}
	synoHealth = map[int64]string{1: "normal", 2: "warning", 3: "critical", 4: "failing"}
	// qnapDisk names NAS-MIB's hdStatus.
	qnapDisk = map[int64]string{0: "ready", -4: "unknown", -5: "no disk", -6: "invalid", -9: "read/write error"}
)

// firstOf returns what the first of the readers finds: each reads one
// vendor's table, which other devices do not have.
func firstOf[T any](w *walker, readers ...func(*walker) ([]T, error)) ([]T, error) {
	for _, read := range readers {
		if out, err := read(w); err != nil || len(out) > 0 {
			return out, err
		}
	}
	return nil, nil
}

// rowsOf reads a table's columns and turns each row into a T; rows that
// are not one (an empty bay) are left out.
func rowsOf[T any](entry string, cols []int, f func(row) (T, bool)) func(*walker) ([]T, error) {
	return func(w *walker) ([]T, error) {
		rows, err := w.table(entry, cols...)
		var out []T
		for _, r := range rows {
			if v, ok := f(r); ok {
				out = append(out, v)
			}
		}
		return out, err
	}
}

// volumeStates reads the health of a device's volumes or pools: Synology's
// RAID table, QNAP's or Asustor's volume table or TrueNAS's pools.
func volumeStates(w *walker) ([]StorageState, error) {
	return firstOf(w, rowsOf(oidSynoRaidEntry, []int{2, 3}, synoVolume), rowsOf(oidQnapEx+".17.1", []int{2, 6}, qnapVolume),
		rowsOf(oidQnapOld+".17.1", []int{2, 6}, qnapVolume), rowsOf(oidAsusVolumeEntry, []int{2, 4}, asusVolume),
		rowsOf(oidZpoolEntry, []int{2, 3}, zpool))
}

func synoVolume(r row) (StorageState, bool) {
	n := num(r.v[3])
	return StorageState{Name: r.v[2], State: cmp.Or(synoRaid[n], "in state "+r.v[3]), OK: n != 11 && n != 12 && n >= 1 && n <= 20,
		Repairing: n == 2}, true
}

func qnapVolume(r row) (StorageState, bool) {
	st := strings.ToLower(strings.TrimSpace(r.v[6]))
	busy := slices.ContainsFunc([]string{"rebuild", "sync", "scrub", "migrat", "expand"}, func(w string) bool { return strings.Contains(st, w) })
	return StorageState{Name: r.v[2], State: cmp.Or(st, "unknown"), OK: st == "ready" || busy, Repairing: strings.Contains(st, "rebuild")}, true
}

func asusVolume(r row) (StorageState, bool) {
	st := strings.ToLower(strings.TrimSpace(r.v[4]))
	busy := slices.Contains([]string{"resyncing", "recovering", "reshaping", "appending", "migrating", "fsexpanding", "fsiniting", "checking"}, st)
	return StorageState{Name: r.v[2], State: cmp.Or(st, "unknown"), OK: st == "healthy" || st == "clean" || st == "active" || busy,
		Repairing: st == "recovering"}, true
}

func zpool(r row) (StorageState, bool) {
	h := strings.ToLower(strings.TrimSpace(r.v[3]))
	return StorageState{Name: r.v[2], State: cmp.Or(h, "unknown"), OK: h == "online"}, true
}

// diskStates reads the health of a device's disks: Synology's disk table,
// QNAP's or Asustor's.
func diskStates(w *walker) ([]StorageState, error) {
	return firstOf(w, rowsOf(oidSynoDiskEntry, []int{2, 5, 13}, synoDiskState), rowsOf(oidQnapEx+".11.1", []int{2, 4, 7}, qnapDiskState),
		rowsOf(oidQnapOld+".11.1", []int{2, 4, 7}, qnapDiskState), rowsOf(oidAsusDiskEntry, []int{2, 5, 6, 7}, asusDiskState))
}

func synoDiskState(r row) (StorageState, bool) {
	n := num(r.v[5])
	st := StorageState{Name: r.v[2], State: cmp.Or(synoDisk[n], "in state "+r.v[5]), OK: n >= 1 && n <= 3}
	// DSM 7.1 and later also judge the disk's health.
	if h, ok := r.v[13]; ok && num(h) != 1 {
		st.State += ", health " + cmp.Or(synoHealth[num(h)], h)
		st.OK = false
	}
	return st, true
}

func qnapDiskState(r row) (StorageState, bool) {
	n := num(r.v[4])
	st := StorageState{Name: r.v[2], State: cmp.Or(qnapDisk[n], "in state "+r.v[4]), OK: n == 0}
	smartState(&st, r.v[7], "good", "normal", "ok", "--")
	return st, n != -5 // -5: an empty bay
}

func asusDiskState(r row) (StorageState, bool) {
	d := asusDisk(r)
	st := StorageState{Name: d.name, State: cmp.Or(d.status, "unknown"), OK: d.status == "good" || d.status == "idle"}
	smartState(&st, d.smart, "healthy", "normal")
	return st, true
}

// smartState adds a disk's SMART status to its state, unless it is empty
// or one of good.
func smartState(st *StorageState, smart string, good ...string) {
	smart = strings.ToLower(strings.TrimSpace(smart))
	if smart != "" && !slices.Contains(good, smart) {
		st.State += ", SMART " + smart
		st.OK = false
	}
}

// asusDisk reads a row of Asustor's disk table.
func asusDisk(r row) (d struct {
	name, status, smart string
	celsius             int
}) {
	d.name, d.status = r.v[2], strings.ToLower(strings.TrimSpace(r.v[5]))
	if t, err := strconv.Atoi(strings.TrimSpace(r.v[6])); err == nil {
		d.celsius = t // before ADM 5.1
	} else {
		d.smart, d.celsius = r.v[6], int(num(r.v[7]))
	}
	return d
}

// states checks the volumes or disks a monitor watches.
func (c *storageChecker) states(w *walker, what, name string, read func(*walker) ([]StorageState, error)) (bool, string, error) {
	all, err := read(w)
	if err != nil {
		return false, "", err
	}
	if len(all) == 0 {
		return false, "", noHealth(what + " health")
	}
	if name != "" {
		i := slices.IndexFunc(all, func(s StorageState) bool { return s.Name == name })
		if i < 0 {
			return false, "", missing(what, name, all, func(s StorageState) string { return s.Name })
		}
		all = all[i : i+1]
	}
	var bad, good []string
	for _, s := range all {
		if s.OK && (!s.Repairing || !c.s.RepairingDown) {
			good = append(good, s.Name+" "+s.State)
		} else {
			bad = append(bad, s.Name+" "+s.State)
		}
	}
	if len(bad) > 0 {
		return false, listText(bad), nil
	}
	return true, listText(good), nil
}

// listText joins at most ten things.
func listText(items []string) string {
	if len(items) > 10 {
		return strings.Join(items[:10], ", ") + fmt.Sprintf(" and %d more", len(items)-10)
	}
	return strings.Join(items, ", ")
}

func noHealth(what string) error {
	return fmt.Errorf("the device reports no %s over SNMP: Synology DSM, QNAP QTS, Asustor ADM and TrueNAS do; "+
		"on others, watch a command that snmpd runs", what)
}

// StorageTemp is a disk's temperature.
type StorageTemp struct {
	Name    string `json:"name"`
	Celsius int    `json:"celsius"`
}

var leadingNumber = regexp.MustCompile(`^\s*(-?\d+)`)

// temperatures reads the temperatures of a device's disks: Synology's disk
// table, QNAP's, Asustor's or TrueNAS's.
func temperatures(w *walker) ([]StorageTemp, error) {
	return firstOf(w, rowsOf(oidSynoDiskEntry, []int{2, 6}, func(r row) (StorageTemp, bool) {
		return StorageTemp{r.v[2], int(num(r.v[6]))}, true
	}), rowsOf(oidQnapEx+".11.1", []int{2, 3, 4}, qnapTemp), rowsOf(oidQnapOld+".11.1", []int{2, 3, 4}, qnapTemp),
		rowsOf(oidAsusDiskEntry, []int{2, 5, 6, 7}, func(r row) (StorageTemp, bool) {
			d := asusDisk(r)
			return StorageTemp{d.name, d.celsius}, true
		}), rowsOf(oidHddTempEntry, []int{2, 3}, func(r row) (StorageTemp, bool) {
			return StorageTemp{r.v[2], int(num(r.v[3]) / 1000)}, true // millidegrees
		}))
}

// qnapTemp reads a QNAP disk's temperature: a number, or in the older
// table "38 C/100 F".
func qnapTemp(r row) (StorageTemp, bool) {
	m := leadingNumber.FindStringSubmatch(r.v[3])
	if num(r.v[4]) == -5 || m == nil {
		return StorageTemp{}, false
	}
	return StorageTemp{r.v[2], int(num(m[1]))}, true
}

func (c *storageChecker) temperature(w *walker) (bool, string, error) {
	s := c.s
	all, err := temperatures(w)
	if err != nil {
		return false, "", err
	}
	if len(all) == 0 {
		return false, "", noHealth("disk temperatures")
	}
	if s.Disk != "" {
		i := slices.IndexFunc(all, func(t StorageTemp) bool { return t.Name == s.Disk })
		if i < 0 {
			return false, "", missing("disk", s.Disk, all, func(t StorageTemp) string { return t.Name })
		}
		all = all[i : i+1]
	}
	var hot []string
	for _, t := range all {
		if t.Celsius > s.MaxCelsius {
			hot = append(hot, fmt.Sprintf("%s %d °C", t.Name, t.Celsius))
		}
	}
	if len(hot) > 0 {
		return false, fmt.Sprintf("Above %d °C: %s", s.MaxCelsius, listText(hot)), nil
	}
	top := slices.MaxFunc(all, func(a, b StorageTemp) int { return cmp.Compare(a.Celsius, b.Celsius) })
	if len(all) == 1 {
		return true, fmt.Sprintf("%s %d °C", top.Name, top.Celsius), nil
	}
	return true, fmt.Sprintf("Hottest of %d disks: %s %d °C", len(all), top.Name, top.Celsius), nil
}

// systemState reads a device's own judgement of its fans, power and
// system: Synology's or QNAP's.
func systemState(w *walker) (StorageState, error) {
	st := StorageState{Name: "System", OK: true}
	var parts []string
	status, err := w.get(oidSynoSystem + ".1.0")
	if err != nil {
		return st, err
	}
	if status != "" {
		for _, p := range []struct{ name, oid string }{{"system", ".1.0"}, {"power", ".3.0"}, {"system fan", ".4.1.0"}, {"CPU fan", ".4.2.0"}} {
			v, err := w.get(oidSynoSystem + p.oid)
			if err != nil {
				return st, err
			}
			if v == "" {
				continue
			}
			state := "normal"
			if v != "1" {
				state, st.OK = "failed", false
			}
			parts = append(parts, p.name+" "+state)
		}
		if t, err := w.get(oidSynoSystem + ".2.0"); err == nil && t != "" {
			parts = append(parts, t+" °C")
		}
		st.State = sentence(strings.Join(parts, ", "))
		return st, nil
	}
	for _, p := range []struct{ name, oid string }{{"fans", oidQnapFanStatus}, {"power supplies", oidQnapPowerStatus}} {
		vals, err := w.walk(p.oid)
		if err != nil {
			return st, err
		}
		failed := 0
		for _, v := range vals {
			if v != "0" {
				failed++
			}
		}
		switch {
		case len(vals) == 0:
		case failed > 0:
			parts, st.OK = append(parts, fmt.Sprintf("%d of %d %s failed", failed, len(vals), p.name)), false
		default:
			parts = append(parts, p.name+" normal")
		}
	}
	if len(parts) == 0 {
		return st, noHealth("system health")
	}
	st.State = sentence(strings.Join(parts, ", "))
	return st, nil
}

// Commands snmpd runs (NET-SNMP-EXTEND-MIB): their rows are indexed by
// the extend line's name, as a string.
const (
	oidExtendCommand = "1.3.6.1.4.1.8072.1.3.2.2.1.2"
	oidExtendLine1   = "1.3.6.1.4.1.8072.1.3.2.3.1.1"
	oidExtendResult  = "1.3.6.1.4.1.8072.1.3.2.3.1.4"
)

// StorageCommand is what a command snmpd runs said.
type StorageCommand struct {
	Name   string `json:"name"`
	Status int    `json:"status"` // its exit status
	Output string `json:"output"` // its first line
}

func (c StorageCommand) message() string {
	out := clipLine(c.Output)
	switch {
	case c.Status == 0:
		return cmp.Or(out, c.Name+" exited with 0")
	case out == "":
		return fmt.Sprintf("%s exited with %d", c.Name, c.Status)
	}
	return fmt.Sprintf("%s (exit status %d)", out, c.Status)
}

// clipLine keeps a command's line to 200 bytes.
func clipLine(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 200 {
		return s
	}
	return strings.ToValidUTF8(s[:200], "")
}

func extendIndex(name string) string {
	parts := []string{strconv.Itoa(len(name))}
	for _, b := range []byte(name) {
		parts = append(parts, strconv.Itoa(int(b)))
	}
	return strings.Join(parts, ".")
}

// extendNameOf reads a name back from its index; "" if it is not one.
func extendNameOf(index string) string {
	parts := strings.Split(index, ".")
	if n, err := strconv.Atoi(parts[0]); err != nil || n != len(parts)-1 || n == 0 {
		return ""
	}
	b := make([]byte, 0, len(parts)-1)
	for _, p := range parts[1:] {
		x, err := strconv.ParseUint(p, 10, 8)
		if err != nil {
			return ""
		}
		b = append(b, byte(x))
	}
	return string(b)
}

func extendResult(w *walker, name string) (StorageCommand, error) {
	cmd := StorageCommand{Name: name}
	idx := extendIndex(name)
	status, err := w.get(oidExtendResult + "." + idx)
	if err != nil {
		return cmd, err
	}
	if status == "" {
		return cmd, fmt.Errorf("snmpd has no extend line named %s: add one to snmpd.conf", name)
	}
	cmd.Status = int(num(status))
	cmd.Output, err = w.get(oidExtendLine1 + "." + idx)
	return cmd, err
}

// Discovery.

// StorageDevice is what an SNMP agent says about its storage.
type StorageDevice struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Volumes     []StorageVolume  `json:"volumes"`
	Pools       []StorageState   `json:"pools"` // volumes' or pools' health
	Disks       []StorageState   `json:"disks"`
	Temps       []StorageTemp    `json:"temps"`
	System      *StorageState    `json:"system,omitempty"`
	Commands    []StorageCommand `json:"commands"`
}

// skipVolumes are the mount points of a system's own memory and devices,
// which are not storage to watch.
var skipVolumes = []string{"/dev", "/run", "/sys", "/proc", "/tmp"}

// DiscoverStorage asks an SNMP agent about its storage, with the settings
// of an snmp or snmp-storage monitor; only how to reach and sign in to it
// is used.
func DiscoverStorage(ctx context.Context, env *Env, settings []byte) (StorageDevice, error) {
	var a SNMPAgent
	if err := json.Unmarshal(settings, &a); err != nil {
		return StorageDevice{}, errors.New("the SNMP settings are not valid")
	}
	if err := a.normalizeAgent(); err != nil {
		return StorageDevice{}, err
	}
	w, err := dialWalker(ctx, env, &a)
	if err != nil {
		return StorageDevice{}, errors.New(dialError(err))
	}
	defer w.conn.Close()
	var d StorageDevice
	if d.Name, err = w.get(oidSysName); err == nil {
		d.Description, err = w.get(oidSysDescr)
	}
	if err != nil {
		return d, err
	}
	if model, _ := w.get(oidSynoModel); model != "" {
		version, _ := w.get(oidSynoVersion)
		d.Description = strings.TrimSuffix("Synology "+model+", "+version, ", ")
	}
	return d, d.read(w)
}

// read reads what a device's storage monitors would watch.
func (d *StorageDevice) read(w *walker) error {
	vols, err := volumes(w)
	if err != nil {
		return err
	}
	for _, v := range vols {
		if !slices.ContainsFunc(skipVolumes, func(p string) bool { return v.Name == p || strings.HasPrefix(v.Name, p+"/") }) {
			d.Volumes = append(d.Volumes, v)
		}
	}
	if d.Pools, err = volumeStates(w); err != nil {
		return err
	}
	if d.Disks, err = diskStates(w); err != nil {
		return err
	}
	if d.Temps, err = temperatures(w); err != nil {
		return err
	}
	if st, err := systemState(w); err == nil {
		d.System = &st
	}
	d.Commands, err = commands(w)
	return err
}

// commands reads what each command snmpd runs said.
func commands(w *walker) ([]StorageCommand, error) {
	cmds, err := w.walk(oidExtendCommand)
	if err != nil {
		return nil, err
	}
	var out []StorageCommand
	for idx := range cmds {
		if name := extendNameOf(idx); name != "" && extendName.MatchString(name) {
			cmd, err := extendResult(w, name)
			if err != nil {
				return nil, err
			}
			out = append(out, cmd)
		}
	}
	slices.SortFunc(out, func(a, b StorageCommand) int { return cmp.Compare(a.Name, b.Name) })
	return out, nil
}

// Suggestion is a monitor discovery suggests.
type Suggestion struct {
	Name     string         // after the device's name
	Detail   string         // how it is now
	Up       bool           // whether it would be up now
	Settings map[string]any // over the device's
}

// Suggestions are the monitors of a device's storage: each volume's space
// and health, its disks' health and temperature, the system's health and
// each command snmpd runs.
func (d StorageDevice) Suggestions() []Suggestion {
	var out []Suggestion
	for _, v := range d.Volumes {
		out = append(out, Suggestion{Name: v.Name + " space", Detail: v.String(), Up: v.Percent <= 90,
			Settings: map[string]any{"watch": WatchCapacity, "volume": v.Name}})
	}
	for _, p := range d.Pools {
		out = append(out, Suggestion{Name: p.Name + " health", Detail: sentence(p.State), Up: p.OK,
			Settings: map[string]any{"watch": WatchVolumeHealth, "volume": p.Name}})
	}
	if len(d.Disks) > 0 {
		var bad []string
		for _, s := range d.Disks {
			if !s.OK {
				bad = append(bad, s.Name+" "+s.State)
			}
		}
		detail := fmt.Sprintf("%d disks, all healthy", len(d.Disks))
		if len(bad) > 0 {
			detail = sentence(listText(bad))
		}
		out = append(out, Suggestion{Name: "disk health", Detail: detail, Up: len(bad) == 0,
			Settings: map[string]any{"watch": WatchDiskHealth, "disk": ""}})
	}
	if len(d.Temps) > 0 {
		top := slices.MaxFunc(d.Temps, func(a, b StorageTemp) int { return cmp.Compare(a.Celsius, b.Celsius) })
		out = append(out, Suggestion{Name: "disk temperature", Detail: fmt.Sprintf("Hottest: %s %d °C", top.Name, top.Celsius),
			Up: top.Celsius <= 50, Settings: map[string]any{"watch": WatchDiskTemperature, "disk": ""}})
	}
	if d.System != nil {
		out = append(out, Suggestion{Name: "system health", Detail: d.System.State, Up: d.System.OK,
			Settings: map[string]any{"watch": WatchSystemHealth}})
	}
	for _, c := range d.Commands {
		out = append(out, Suggestion{Name: c.Name, Detail: sentence(c.message()), Up: c.Status == 0,
			Settings: map[string]any{"watch": WatchCommand, "command": c.Name}})
	}
	return out
}

// bigMul is a*b, for sizes that may not fit 64 bits; capped at the
// largest int64.
func bigMul(a, b int64) int64 {
	p := new(big.Int).Mul(big.NewInt(a), big.NewInt(b))
	if !p.IsInt64() {
		return 1<<63 - 1
	}
	return p.Int64()
}
