// SPDX-License-Identifier: AGPL-3.0-or-later

package workload

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The in-cluster ServiceAccount's files.
const serviceAccount = "/var/run/secrets/kubernetes.io/serviceaccount"

// Kubernetes lists a cluster's Deployments, StatefulSets and DaemonSets,
// through its API with a ServiceAccount token.
type Kubernetes struct {
	base  string
	token func() (string, error) // read for every request: the kubelet rotates it
	http  *http.Client
	// Namespaces limits the listing; empty: every namespace.
	Namespaces []string
}

// NewKubernetes returns a client for the API at base, trusting caPEM.
func NewKubernetes(base string, caPEM []byte, token func() (string, error)) (*Kubernetes, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("kubernetes: no CA certificate")
	}
	return &Kubernetes{
		base: strings.TrimRight(base, "/"), token: token,
		http: &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		}},
	}, nil
}

// InCluster returns a client for the cluster the agent's pod runs in, with
// the pod's ServiceAccount. getenv reads the environment.
func InCluster(getenv func(string) string, dir string) (*Kubernetes, error) {
	host, port := getenv("KUBERNETES_SERVICE_HOST"), getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, errors.New("not running in a Kubernetes pod (KUBERNETES_SERVICE_HOST is not set)")
	}
	if dir == "" {
		dir = serviceAccount
	}
	// The ServiceAccount's own file, in a fixed folder.
	// nosemgrep
	ca, err := os.ReadFile(filepath.Join(dir, "ca.crt")) //nolint:gosec // G304: the ServiceAccount's own file
	if err != nil {
		return nil, fmt.Errorf("kubernetes: %w", err)
	}
	return NewKubernetes("https://"+net.JoinHostPort(host, port), ca, func() (string, error) {
		// nosemgrep
		b, err := os.ReadFile(filepath.Join(dir, "token")) //nolint:gosec // G304: the ServiceAccount's own file
		return strings.TrimSpace(string(b)), err
	})
}

func (k *Kubernetes) get(ctx context.Context, path string, out any) error {
	token, err := k.token()
	if err != nil {
		return fmt.Errorf("kubernetes: the ServiceAccount token: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	resp, err := k.http.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach the Kubernetes API: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return apiError("Kubernetes "+path, resp, jsonMessage)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, MaxBody)).Decode(out); err != nil {
		return fmt.Errorf("kubernetes %s: %w", path, errNoBody)
	}
	return nil
}

// kind is a kind of workload in the API.
type kind struct{ name, plural string }

var kinds = []kind{{"deployment", "deployments"}, {"statefulset", "statefulsets"}, {"daemonset", "daemonsets"}}

type object struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec struct {
		Replicas *int64 `json:"replicas"`
	} `json:"spec"`
	Status struct {
		ReadyReplicas          int64 `json:"readyReplicas"`
		DesiredNumberScheduled int64 `json:"desiredNumberScheduled"`
		NumberReady            int64 `json:"numberReady"`
	} `json:"status"`
}

// List returns the workloads in Namespaces, or in every namespace.
func (k *Kubernetes) List(ctx context.Context) ([]Workload, error) {
	nss := k.Namespaces
	if len(nss) == 0 {
		nss = []string{""}
	}
	out := []Workload{}
	for _, ns := range nss {
		for _, kd := range kinds {
			p := "/apis/apps/v1/" + kd.plural
			if ns != "" {
				p = "/apis/apps/v1/namespaces/" + url.PathEscape(ns) + "/" + kd.plural
			}
			var list struct{ Items []object }
			if err := k.get(ctx, p, &list); err != nil {
				return nil, err
			}
			for _, o := range list.Items {
				out = append(out, o.workload(kd.name))
			}
		}
	}
	sortWorkloads(out)
	return out, nil
}

func (o object) workload(kind string) Workload {
	m := o.Metadata
	w := Workload{Key: m.Namespace + "/" + kind + "/" + m.Name, Name: m.Name, Kind: kind, Group: m.Namespace}
	if kind == "daemonset" {
		if o.Status.DesiredNumberScheduled <= 0 {
			w.Status = "Scheduled on no nodes"
			return w
		}
		w.Up, w.Status = ready(o.Status.NumberReady, o.Status.DesiredNumberScheduled, "ready")
		return w
	}
	want := int64(1) // the API's default
	if o.Spec.Replicas != nil {
		want = *o.Spec.Replicas
	}
	w.Up, w.Status = ready(o.Status.ReadyReplicas, want, "ready")
	return w
}
