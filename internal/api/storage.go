// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/zjcochran/eyrie/internal/check"
)

// maxStoragePoints is the most points of storage history one answer has.
const maxStoragePoints = 300

// storagePoint is the storage of a device at one time: each volume's use
// and each disk's temperature.
type storagePoint struct {
	At      time.Time          `json:"at"`
	Volumes map[string]float64 `json:"volumes"` // percent used
	Temps   map[string]int     `json:"temps"`   // degrees Celsius
}

// storage answers what an SNMP storage monitor last read of its device,
// and how its volumes' use and disks' temperatures went over ?days=
// (default 1, most 35).
func (s *Server) storage(w http.ResponseWriter, r *http.Request) {
	m, ok := s.monitorFor(w, r)
	if !ok {
		return
	}
	days := 1
	if n, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && n > 0 {
		days = min(n, 35)
	}
	samples, err := s.Store.StorageSamples(r.Context(), m.ID, s.now().Add(-time.Duration(days)*24*time.Hour))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	out := struct {
		At      *time.Time           `json:"at"`
		Device  *check.StorageDevice `json:"device"`
		History []storagePoint       `json:"history"`
	}{History: []storagePoint{}}
	step := max(1, (len(samples)+maxStoragePoints-1)/maxStoragePoints)
	for i := len(samples) - 1; i >= 0 && out.Device == nil; i-- {
		var d check.StorageDevice
		if json.Unmarshal(samples[i].Data, &d) == nil {
			at := samples[i].At
			out.At, out.Device = &at, &d
		}
	}
	for i, smp := range samples {
		if i%step != 0 && i != len(samples)-1 {
			continue
		}
		var d check.StorageDevice
		if json.Unmarshal(smp.Data, &d) != nil {
			continue
		}
		p := storagePoint{At: smp.At, Volumes: map[string]float64{}, Temps: map[string]int{}}
		for _, v := range d.Volumes {
			p.Volumes[v.Name] = v.Percent
		}
		for _, t := range d.Temps {
			p.Temps[t.Name] = t.Celsius
		}
		out.History = append(out.History, p)
	}
	writeJSON(w, http.StatusOK, out)
}
