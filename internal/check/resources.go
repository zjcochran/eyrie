// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Usage is how busy a host is, in percent.
type Usage struct {
	CPU    float64 `json:"cpu"`
	Memory float64 `json:"memory"`
}

// Resources is the settings of the system-resources type: the CPU and
// memory use of the host the check runs on, read from Linux's /proc. In a
// container, those files show the whole host, unless something such as
// LXCFS gives the container figures of its own.
type Resources struct {
	// CPUMax and MemoryMax are the highest use, in percent, that is up; 0:
	// no limit.
	CPUMax    float64 `json:"cpu_max"`
	MemoryMax float64 `json:"memory_max"`
}

// cpuSample is how long the CPU is watched: its use is the share of this
// time it was busy.
var cpuSample = time.Second

func (r *Resources) normalize() error {
	for _, v := range []struct {
		name string
		max  float64
	}{{"CPU", r.CPUMax}, {"memory", r.MemoryMax}} {
		if v.max < 0 || v.max > 100 {
			return fmt.Errorf("the %s limit must be from 0 (none) to 100%%", v.name)
		}
	}
	return nil
}

func (r *Resources) checker(env *Env) Checker { return &resourcesChecker{r: r, env: env} }

func (r *Resources) describe() []Fact {
	limit := func(v float64) string {
		if v == 0 {
			return "none"
		}
		return strconv.FormatFloat(v, 'f', -1, 64) + "%"
	}
	return []Fact{{"CPU limit", limit(r.CPUMax)}, {"Memory limit", limit(r.MemoryMax)}}
}

type resourcesChecker struct {
	r   *Resources
	env *Env
}

func (c *resourcesChecker) Check(ctx context.Context) Result {
	if c.env.OS != "linux" {
		return fail("System resources can be read only on Linux, not %s", c.env.OS)
	}
	busy1, total1, err := c.cpuTimes()
	if err != nil {
		return failErr(err)
	}
	t := time.NewTimer(cpuSample)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return fail("Timed out while measuring the CPU")
	case <-t.C:
	}
	busy2, total2, err := c.cpuTimes()
	if err != nil {
		return failErr(err)
	}
	var u Usage
	if total2 > total1 {
		u.CPU = 100 * float64(busy2-busy1) / float64(total2-total1)
	}
	used, total, err := c.memory()
	if err != nil {
		return failErr(err)
	}
	u.Memory = 100 * float64(used) / float64(total)
	msg := fmt.Sprintf("CPU %.1f%%, memory %.1f%% of %s", u.CPU, u.Memory, gib(total))
	if load, err := c.load(); err == nil {
		msg += ", load " + load
	}
	res := Result{OK: true, Message: msg, Latency: -1, Usage: &u}
	var over []string
	if c.r.CPUMax > 0 && u.CPU > c.r.CPUMax {
		over = append(over, fmt.Sprintf("CPU is above %s%%", strconv.FormatFloat(c.r.CPUMax, 'f', -1, 64)))
	}
	if c.r.MemoryMax > 0 && u.Memory > c.r.MemoryMax {
		over = append(over, fmt.Sprintf("memory is above %s%%", strconv.FormatFloat(c.r.MemoryMax, 'f', -1, 64)))
	}
	if over != nil {
		res.OK = false
		res.Message = sentence(strings.Join(over, " and ")) + ": " + msg
	}
	return res
}

// cpuTimes reads the time every CPU was busy, and the total, in ticks.
// Waiting for I/O counts as idle; guest time is already counted as user
// time.
func (c *resourcesChecker) cpuTimes() (busy, total uint64, err error) {
	raw, err := c.env.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, fmt.Errorf("cannot read the CPU times: %w", err)
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0, errors.New("/proc/stat has no CPU times")
	}
	// user nice system idle iowait irq softirq steal
	for i, s := range f[1:min(len(f), 9)] {
		n, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return 0, 0, errors.New("/proc/stat has no CPU times")
		}
		total += n
		if i != 3 && i != 4 {
			busy += n
		}
	}
	return busy, total, nil
}

// memory reads the memory in use and in all, in bytes: what is not
// available to start programs without swapping counts as used.
func (c *resourcesChecker) memory() (used, total uint64, err error) {
	raw, err := c.env.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, fmt.Errorf("cannot read the memory figures: %w", err)
	}
	var avail uint64
	var haveAvail bool
	for _, line := range strings.Split(string(raw), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		n, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			continue
		}
		switch k {
		case "MemTotal":
			total = n * 1024
		case "MemAvailable":
			avail, haveAvail = n*1024, true
		}
	}
	if total == 0 || !haveAvail || avail > total {
		return 0, 0, errors.New("/proc/meminfo has no MemTotal and MemAvailable")
	}
	return total - avail, total, nil
}

// load reads the load average over the last minute.
func (c *resourcesChecker) load() (string, error) {
	raw, err := c.env.ReadFile("/proc/loadavg")
	if err != nil {
		return "", err
	}
	f := strings.Fields(string(raw))
	if len(f) == 0 {
		return "", errors.New("no load average")
	}
	return f[0], nil
}

// gib is a number of bytes in GiB.
func gib(b uint64) string {
	return strconv.FormatFloat(float64(b)/(1<<30), 'f', 1, 64) + " GiB"
}
