// SPDX-License-Identifier: AGPL-3.0-or-later

package check

import (
	"context"
	"encoding/json"
	"io/fs"
	"testing"
	"time"
)

// procFiles answers ReadFile from a table, giving each /proc/stat in turn.
func procFiles(stats []string, meminfo, loadavg string) func(string) ([]byte, error) {
	return func(name string) ([]byte, error) {
		switch name {
		case "/proc/stat":
			if len(stats) == 0 {
				return nil, fs.ErrNotExist
			}
			s := stats[0]
			stats = stats[1:]
			return []byte(s), nil
		case "/proc/meminfo":
			if meminfo == "" {
				return nil, fs.ErrNotExist
			}
			return []byte(meminfo), nil
		case "/proc/loadavg":
			if loadavg == "" {
				return nil, fs.ErrNotExist
			}
			return []byte(loadavg), nil
		}
		return nil, fs.ErrNotExist
	}
}

const (
	stat1   = "cpu  100 0 100 700 100 0 0 0 0 0\ncpu0 100 0 100 700 100 0 0 0 0 0\n"
	stat2   = "cpu  150 0 150 800 100 0 0 0 0 0\ncpu0 150 0 150 800 100 0 0 0 0 0\n"
	meminfo = "MemTotal:       16777216 kB\nMemFree:         1048576 kB\nMemAvailable:    4194304 kB\nBuffers: x\n\nHugePages_Total:       0\n"
	loadavg = "0.52 0.48 0.40 1/123 4567\n"
)

func resourcesEnv(stats []string, mem, load string) *Env {
	env := testEnv()
	env.OS = "linux"
	env.ReadFile = procFiles(stats, mem, load)
	return env
}

func TestResources(t *testing.T) {
	old := cpuSample
	cpuSample = time.Millisecond
	t.Cleanup(func() { cpuSample = old })

	// 100 of 200 ticks were busy; 12 of 16 GiB are in use.
	r := run(t, resourcesEnv([]string{stat1, stat2}, meminfo, loadavg), "system-resources", `{}`)
	if !r.OK || r.Message != "CPU 50.0%, memory 75.0% of 16.0 GiB, load 0.52" || r.Latency != -1 ||
		r.Usage == nil || r.Usage.CPU != 50 || r.Usage.Memory != 75 {
		t.Errorf("no limits: %+v", r)
	}
	r = run(t, resourcesEnv([]string{stat1, stat2}, meminfo, ""), "system-resources", `{"cpu_max":40,"memory_max":80}`)
	if r.OK || r.Message != "CPU is above 40%: CPU 50.0%, memory 75.0% of 16.0 GiB" || r.Usage == nil {
		t.Errorf("CPU over: %+v", r)
	}
	r = run(t, resourcesEnv([]string{stat1, stat2}, meminfo, loadavg), "system-resources", `{"cpu_max":40,"memory_max":70.5}`)
	if r.OK || r.Message != "CPU is above 40% and memory is above 70.5%: CPU 50.0%, memory 75.0% of 16.0 GiB, load 0.52" {
		t.Errorf("both over: %+v", r)
	}
	// No time passed between the readings.
	if r := run(t, resourcesEnv([]string{stat1, stat1}, meminfo, "\n"), "system-resources", `{}`); !r.OK || r.Usage.CPU != 0 {
		t.Errorf("idle: %+v", r)
	}

	for _, c := range []struct {
		name  string
		env   *Env
		wants string
	}{
		{"not Linux", &Env{OS: "darwin"}, "System resources can be read only on Linux, not darwin"},
		{"no stat", resourcesEnv(nil, meminfo, loadavg), "Cannot read the CPU times: file does not exist"},
		{"no second stat", resourcesEnv([]string{stat1}, meminfo, loadavg), "Cannot read the CPU times: file does not exist"},
		{"bad stat", resourcesEnv([]string{"intr 1 2 3\n"}, meminfo, loadavg), "/proc/stat has no CPU times"},
		{"bad numbers", resourcesEnv([]string{"cpu a b c d e\n"}, meminfo, loadavg), "/proc/stat has no CPU times"},
		{"no meminfo", resourcesEnv([]string{stat1, stat2}, "", loadavg), "Cannot read the memory figures: file does not exist"},
		{"old meminfo", resourcesEnv([]string{stat1, stat2}, "MemTotal: 1024 kB\nMemFree: 512 kB\n", loadavg),
			"/proc/meminfo has no MemTotal and MemAvailable"},
	} {
		_, ch, err := Parse(c.env, "system-resources", json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		if r := ch.Check(context.Background()); r.OK || r.Message != c.wants {
			t.Errorf("%s: %+v", c.name, r)
		}
	}

	// The check ends with its time.
	cpuSample = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, ch, _ := Parse(resourcesEnv([]string{stat1, stat2}, meminfo, loadavg), "system-resources", json.RawMessage(`{}`))
	if r := ch.Check(ctx); r.OK || r.Message != "Timed out while measuring the CPU" {
		t.Errorf("timed out: %+v", r)
	}
}

func TestResourcesSettings(t *testing.T) {
	env := testEnv()
	for _, s := range []string{`{"cpu_max":-1}`, `{"memory_max":101}`} {
		if _, _, err := Parse(env, "system-resources", json.RawMessage(s)); err == nil {
			t.Errorf("%s accepted", s)
		}
	}
	got := Describe("system-resources", json.RawMessage(`{"cpu_max":90.5}`))
	if len(got) != 2 || got[0] != (Fact{"CPU limit", "90.5%"}) || got[1] != (Fact{"Memory limit", "none"}) {
		t.Errorf("describe %v", got)
	}
}
