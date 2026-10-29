// SPDX-License-Identifier: AGPL-3.0-or-later

// Package check knows the monitor types: their settings, how to check them,
// and what a check found. Every check reaches the network only through an
// Env, so tests give it fakes.
package check

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/zjcochran/eyrie/internal/workload"
)

// Result is what one check found.
type Result struct {
	OK          bool
	Message     string
	Latency     time.Duration // negative: none
	CertExpires time.Time     // HTTPS: when the server's certificate expires
	Usage       *Usage        // system resources: the host's use; nil: none
}

func fail(format string, args ...any) Result {
	return Result{Message: fmt.Sprintf(format, args...), Latency: -1}
}

// failErr is a failed check whose message is an error.
func failErr(err error) Result { return fail("%s", sentence(err.Error())) }

// sentence starts a message with a capital letter.
func sentence(s string) string {
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// errTimeout is what a check says when its time is up.
var errTimeout = errors.New("timed out")

// Checker checks one monitor. The context ends at the monitor's timeout.
type Checker interface {
	Check(ctx context.Context) Result
}

// Env is how checks reach the network.
type Env struct {
	// Dial opens TCP and UDP connections.
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
	// Transport makes HTTP requests; insecure skips TLS certificate checks.
	Transport func(insecure bool) http.RoundTripper
	// Resolve looks up a host name's addresses.
	Resolve func(ctx context.Context, host string) ([]netip.Addr, error)
	// Ping sends one ICMP echo request of size bytes and waits for its
	// reply.
	Ping func(ctx context.Context, addr netip.Addr, size, seq int) (time.Duration, error)
	// Run runs a program (never through a shell) and returns its output
	// and error output.
	Run func(ctx context.Context, name string, args ...string) ([]byte, error)
	// OS is the operating system Eyrie runs on, as runtime.GOOS.
	OS string
	// ReadFile reads a file, such as /proc/stat.
	ReadFile func(name string) ([]byte, error)
	// Secrets opens the monitors' sealed secrets; nil: they are not sealed.
	Secrets Sealer
	// UserAgent is sent with HTTP requests.
	UserAgent string
	// Workloads lists the workloads of the platform an agent was set up
	// for; nil in Eyrie, where the types that need it are not checked.
	Workloads workload.Lister
	// DockerHost replaces the default Docker host of docker monitors: an
	// agent's DOCKER_HOST. "": the monitor's own.
	DockerHost string
}

// DefaultEnv reaches the real network.
func DefaultEnv(userAgent string) *Env {
	dialer := &net.Dialer{}
	secure := newTransport(dialer, false)
	insecure := newTransport(dialer, true)
	return &Env{
		Dial: dialer.DialContext,
		Transport: func(skip bool) http.RoundTripper {
			if skip {
				return insecure
			}
			return secure
		},
		Resolve: func(ctx context.Context, h string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", h)
		},
		Ping:      ping,
		Run:       runProgram,
		OS:        runtime.GOOS,
		ReadFile:  os.ReadFile,
		UserAgent: userAgent,
	}
}

func runProgram(ctx context.Context, name string, args ...string) ([]byte, error) {
	// The program is fixed by the monitor type, its arguments are checked,
	// and no shell runs them.
	// nosemgrep
	return exec.CommandContext(ctx, name, args...).CombinedOutput() //nolint:gosec // G204: the program is fixed by the monitor type and its arguments are checked
}

// Type is a monitor type.
type Type struct {
	Name  string
	Label string
	// Scheduled types are checked at their interval by a Checker. The
	// others are run by the engine: push waits for pushes, manual is set by
	// people.
	Scheduled bool
	// settings returns a new, empty settings value of the type.
	settings func() settings
}

// settings are a type's own settings.
type settings interface {
	// normalize fills in defaults and checks the values.
	normalize() error
	// checker is what checks a monitor with these settings.
	checker(env *Env) Checker
	// describe lists the settings that may leave Eyrie, for alert
	// analysis: never header values, bodies or credentials.
	describe() []Fact
}

// Types are every monitor type, by name.
var Types = map[string]Type{}

func register(t Type) { Types[t.Name] = t }

func init() {
	register(Type{"http", "HTTP(S)", true, func() settings { return &HTTP{} }})
	register(Type{"keyword", "HTTP(S) keyword", true, func() settings { return &Keyword{} }})
	register(Type{"json-query", "HTTP(S) JSON query", true, func() settings { return &JSONQuery{} }})
	register(Type{"port", "TCP port", true, func() settings { return &Port{} }})
	register(Type{"ping", "Ping", true, func() settings { return &Ping{} }})
	register(Type{"dns", "DNS", true, func() settings { return &DNS{} }})
	register(Type{"websocket-upgrade", "WebSocket upgrade", true, func() settings { return &WebSocket{} }})
	register(Type{"smtp", "SMTP", true, func() settings { return &SMTP{} }})
	register(Type{"ntp", "NTP", true, func() settings { return &NTP{} }})
	register(Type{"radius", "RADIUS", true, func() settings { return &Radius{} }})
	register(Type{"snmp", "SNMP", true, func() settings { return &SNMP{} }})
	register(Type{"docker", "Docker container", true, func() settings { return &Docker{} }})
	register(Type{"docker-swarm", "Docker Swarm service", true, func() settings { return &Swarm{} }})
	register(Type{"kubernetes", "Kubernetes workload", true, func() settings { return &Kubernetes{} }})
	register(Type{"proxmox", "Proxmox VE guest", true, func() settings { return &Proxmox{} }})
	register(Type{"sip-options", "SIP OPTIONS", true, func() settings { return &SIP{} }})
	register(Type{"tailscale-ping", "Tailscale ping", true, func() settings { return &Tailscale{} }})
	register(Type{"system-service", "System service", true, func() settings { return &Service{} }})
	register(Type{"system-resources", "System resources", true, func() settings { return &Resources{} }})
	register(Type{"pm2", "PM2 process", true, func() settings { return &PM2{} }})
	register(Type{"globalping", "Globalping", true, func() settings { return &Globalping{} }})
	register(Type{"sftp", "SFTP", true, func() settings { return &SFTP{} }})
	register(Type{"steam", "Steam game server", true, func() settings { return &steam{} }})
	register(Type{"gamedig", "Game server", true, func() settings { return &Game{} }})
	register(Type{"grpc-keyword", "gRPC keyword", true, func() settings { return &GRPC{} }})
	register(Type{"real-browser", "HTTP(S) in a real browser", true, func() settings { return &Browser{} }})
	register(Type{"postgres", "PostgreSQL", true, func() settings { return &SQL{kind: "postgres"} }})
	register(Type{"mysql", "MySQL / MariaDB", true, func() settings { return &SQL{kind: "mysql"} }})
	register(Type{"sqlserver", "Microsoft SQL Server", true, func() settings { return &SQL{kind: "sqlserver"} }})
	register(Type{"oracledb", "Oracle Database", true, func() settings { return &SQL{kind: "oracledb"} }})
	register(Type{"mongodb", "MongoDB", true, func() settings { return &MongoDB{} }})
	register(Type{"redis", "Redis", true, func() settings { return &Redis{} }})
	register(Type{"mqtt", "MQTT", true, func() settings { return &MQTT{} }})
	register(Type{"rabbitmq", "RabbitMQ", true, func() settings { return &RabbitMQ{} }})
	register(Type{"kafka-producer", "Kafka producer", true, func() settings { return &Kafka{} }})
	register(Type{"push", "Push", false, func() settings { return &none{} }})
	register(Type{"manual", "Manual", false, func() settings { return &none{} }})
}

// Names are the type names, sorted.
func Names() []string {
	var out []string
	for n := range Types {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// Parse reads a monitor's saved settings, fills in defaults and checks
// them. It returns the settings as they are saved, and their Checker (nil
// for types the engine runs), which has the secrets opened. Errors are
// meant for people.
func Parse(env *Env, typ string, raw json.RawMessage) (json.RawMessage, Checker, error) {
	t, s, err := decode(typ, raw)
	if err != nil {
		return nil, nil, err
	}
	if err := s.normalize(); err != nil {
		return nil, nil, err
	}
	out, err := json.Marshal(s)
	if err != nil {
		return nil, nil, err
	}
	if !t.Scheduled {
		return out, nil, nil
	}
	if err := open(env, s); err != nil {
		return nil, nil, err
	}
	return out, s.checker(env), nil
}

// Opened returns a monitor's saved settings with their secrets opened, for
// an agent to check it with.
func Opened(env *Env, typ string, saved json.RawMessage) (json.RawMessage, error) {
	_, s, err := decode(typ, saved)
	if err != nil {
		return nil, err
	}
	if err := s.normalize(); err != nil {
		return nil, err
	}
	if err := open(env, s); err != nil {
		return nil, err
	}
	return json.Marshal(s)
}

// none is the settings of types without any.
type none struct{}

func (*none) normalize() error     { return nil }
func (*none) checker(*Env) Checker { return nil }
func (*none) describe() []Fact     { return nil }

// host checks a host name or IP address.
func host(h string) (string, error) {
	h = strings.TrimSpace(h)
	if h == "" {
		return "", errors.New("the host is required")
	}
	if strings.ContainsAny(h, " /:?#@") && !isIPv6(h) {
		return "", fmt.Errorf("%q is not a host name or IP address", h)
	}
	return h, nil
}

func isIPv6(h string) bool {
	a, err := netip.ParseAddr(strings.Trim(h, "[]"))
	return err == nil && a.Is6()
}
