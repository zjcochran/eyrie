// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/zjcochran/eyrie/internal/agentauth"
	"github.com/zjcochran/eyrie/internal/agents"
	"github.com/zjcochran/eyrie/internal/store"
	"github.com/zjcochran/eyrie/internal/workload"
)

// ---- the agents' API: signed requests, no session ----

// agentError answers an agent's failed request.
func (s *Server) agentError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, agents.ErrRefused):
		w.Header().Set("WWW-Authenticate", agentauth.Scheme)
		invalid(w, http.StatusUnauthorized, err.Error())
	case errors.Is(err, agents.ErrInvalid):
		invalid(w, http.StatusBadRequest, err.Error())
	default:
		s.fail(w, r, err)
	}
}

// agentBody reads an agent request's body, at most agentauth.MaxBody.
func agentBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	b, err := io.ReadAll(http.MaxBytesReader(w, r.Body, agentauth.MaxBody))
	if err != nil {
		invalid(w, http.StatusRequestEntityTooLarge, "The request is too large.")
		return nil, false
	}
	return b, true
}

func (s *Server) agentEnroll(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if s.enrollLimit.blocked(s.now(), ip) {
		invalid(w, http.StatusTooManyRequests, "Too many enrollment attempts: wait a minute.")
		return
	}
	body, ok := agentBody(w, r)
	if !ok {
		return
	}
	var req agentauth.EnrollRequest
	if err := json.Unmarshal(body, &req); err != nil {
		invalid(w, http.StatusBadRequest, "The request is not an enrollment.")
		return
	}
	a, err := s.Agents.Enroll(r.Context(), req, ip)
	if err != nil {
		if errors.Is(err, agents.ErrRefused) {
			s.enrollLimit.fail(s.now(), ip)
			s.auditWarn(r, "agent enrollment refused", "hostname", req.Hostname)
		}
		s.agentError(w, r, err)
		return
	}
	s.audit(r, "agent enrolled", "agent", a.Name, "hostname", a.Hostname, "platform", a.Platform, "version", a.Version)
	writeJSON(w, http.StatusOK, agentauth.EnrollResponse{Agent: a.ID, Name: a.Name})
}

// agentRequest reads and authenticates a signed request into v.
func (s *Server) agentRequest(w http.ResponseWriter, r *http.Request, v any) (store.Agent, agentauth.PublicKey, bool) {
	body, ok := agentBody(w, r)
	if !ok {
		return store.Agent{}, nil, false
	}
	a, key, err := s.Agents.Authenticate(r.Context(), r, body)
	if err != nil {
		if errors.Is(err, agents.ErrRefused) && s.Agents.LogRefusal(a.ID) {
			name := a.Name
			if name == "" {
				name = "unknown"
			}
			s.auditWarn(r, "agent refused", "agent", name, "reason", err.Error())
		}
		s.agentError(w, r, err)
		return a, nil, false
	}
	if err := json.Unmarshal(body, v); err != nil {
		invalid(w, http.StatusBadRequest, "The request's body is not what was expected.")
		return a, nil, false
	}
	return a, key, true
}

func (s *Server) agentReport(w http.ResponseWriter, r *http.Request) {
	var rep agentauth.Report
	a, _, ok := s.agentRequest(w, r, &rep)
	if !ok {
		return
	}
	if err := s.Agents.Report(r.Context(), a, rep, s.clientIP(r)); err != nil {
		s.agentError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// agentJobs answers an agent with the checks it is to run, waiting up to
// agentauth.JobsWait for one.
func (s *Server) agentJobs(w http.ResponseWriter, r *http.Request) {
	var req struct{}
	a, _, ok := s.agentRequest(w, r, &req)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, agentauth.Jobs{Jobs: s.Agents.Jobs(r.Context(), a.ID, s.jobsWait)})
}

// agentResults takes the results of an agent's checks.
func (s *Server) agentResults(w http.ResponseWriter, r *http.Request) {
	var req agentauth.Results
	a, _, ok := s.agentRequest(w, r, &req)
	if !ok {
		return
	}
	if err := s.Agents.Deliver(a.ID, req.Results); err != nil {
		s.agentError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) agentRotate(w http.ResponseWriter, r *http.Request) {
	var req agentauth.RotateRequest
	a, key, ok := s.agentRequest(w, r, &req)
	if !ok {
		return
	}
	if err := s.Agents.Rotate(r.Context(), a, key, req); err != nil {
		s.agentError(w, r, err)
		return
	}
	if req.NewKey != a.PublicKey {
		k, _ := agentauth.ParsePublicKey(req.NewKey) // checked by Rotate
		s.audit(r, "agent key rotated", "agent", a.Name, "key", k.Fingerprint())
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- managing agents: admins ----

type agentView struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	Status       string     `json:"status"` // pending, online or offline
	Platform     string     `json:"platform"`
	Hostname     string     `json:"hostname"`
	Version      string     `json:"version"`
	LastSeen     *time.Time `json:"last_seen"` // null: never
	LastAddress  string     `json:"last_address"`
	Error        string     `json:"error"`
	Workloads    int        `json:"workloads"`
	Key          string     `json:"key"` // the key's fingerprint; "": not enrolled
	KeyCreated   *time.Time `json:"key_created"`
	TokenExpires *time.Time `json:"token_expires"` // null: no token waits
	CreatedAt    time.Time  `json:"created_at"`
}

func (s *Server) viewAgent(a store.Agent) agentView {
	v := agentView{
		ID: a.ID, Name: a.Name, Status: "pending", Platform: a.Platform, Hostname: a.Hostname, Version: a.Version,
		LastSeen: timeOrNil(a.LastSeen), LastAddress: a.LastAddress, Error: a.Error, Workloads: len(a.Workloads),
		KeyCreated: timeOrNil(a.KeyCreated), TokenExpires: timeOrNil(a.TokenExpires), CreatedAt: a.CreatedAt,
	}
	if k, err := agentauth.ParsePublicKey(a.PublicKey); err == nil {
		v.Key, v.Status = k.Fingerprint(), "offline"
		if agents.Online(a, s.now()) {
			v.Status = "online"
		}
	}
	return v
}

func (s *Server) listAgents(w http.ResponseWriter, _ *http.Request) {
	out := []agentView{}
	for _, a := range s.Agents.List() {
		out = append(out, s.viewAgent(a))
	}
	writeJSON(w, http.StatusOK, out)
}

// newAgentToken is an agent with its one-time enrollment token.
type newAgentToken struct {
	Agent agentView `json:"agent"`
	Token string    `json:"token"`
}

func (s *Server) addAgent(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	a, token, err := s.Agents.Add(r.Context(), body.Name)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	s.audit(r, "agent added", "agent", a.Name, "by", sessionFrom(r).User.Username)
	writeJSON(w, http.StatusCreated, newAgentToken{s.viewAgent(a), token})
}

func (s *Server) renewAgentToken(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	token, err := s.Agents.NewToken(r.Context(), id)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	a, _ := s.Agents.Get(id)
	s.audit(r, "agent enrollment token made", "agent", a.Name, "by", sessionFrom(r).User.Username)
	writeJSON(w, http.StatusOK, newAgentToken{s.viewAgent(a), token})
}

func (s *Server) removeAgent(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	name, err := s.Agents.Remove(r.Context(), id)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	s.audit(r, "agent removed", "agent", name, "by", sessionFrom(r).User.Username)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) agentWorkloads(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	a, found := s.Agents.Get(id)
	if !found {
		invalid(w, http.StatusNotFound, "Not found.")
		return
	}
	ws := a.Workloads
	if ws == nil {
		ws = []workload.Workload{}
	}
	writeJSON(w, http.StatusOK, ws)
}
