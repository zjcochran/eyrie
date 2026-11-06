// SPDX-License-Identifier: AGPL-3.0-or-later

// Package update checks GitHub for a newer release of Eyrie, when an admin
// turns it on (it is off by default). It asks the GitHub API for the
// repository's latest release at a set interval, through a proxy when one
// is set, and keeps what it found for the web app's reminder. A newer
// release can also be sent once to chosen notification channels. It also
// notices when Eyrie starts on a newer version than before, for the web
// app's one-time "updated" window, and fetches the notes of the releases
// since then while checks are on.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zjcochran/eyrie/internal/store"
)

// SettingKey is where the settings are saved; statusKey is where the last
// check's result is kept across restarts.
const (
	SettingKey = "updates"
	statusKey  = "update_status"
	// versionKey is the version that last started; upgradeKey the last
	// start on a newer one.
	versionKey = "running_version"
	upgradeKey = "upgrade"
)

// maxReleases is how many releases' notes an upgrade keeps, newest first.
const maxReleases = 20

// retryNotes is how long to wait before fetching an upgrade's notes again
// after a failure.
const retryNotes = time.Hour

// Settings is how and whether Eyrie checks for updates.
type Settings struct {
	Enabled       bool `json:"enabled"`
	IntervalHours int  `json:"interval_hours"`
	// Proxy is the proxy's URL (http, https or socks5); "" uses the
	// HTTPS_PROXY and NO_PROXY environment variables, if set.
	Proxy         string `json:"proxy"`
	ProxyUser     string `json:"proxy_user"`
	ProxyPassword string `json:"proxy_password"` // sealed
	// Notify sends a newer release to Channels, once per release.
	Notify   bool    `json:"notify"`
	Channels []int64 `json:"channels"`
}

// DefaultSettings is off, checking once a day when turned on.
func DefaultSettings() Settings { return Settings{IntervalHours: 24} }

// Normalize checks the settings. Errors are meant for people.
func (s *Settings) Normalize() error {
	if s.IntervalHours < 1 || s.IntervalHours > 720 {
		return errors.New("the check interval must be 1 to 720 hours")
	}
	s.Proxy = strings.TrimSpace(s.Proxy)
	s.ProxyUser = strings.TrimSpace(s.ProxyUser)
	if s.Proxy != "" {
		u, err := url.Parse(s.Proxy)
		if err != nil || u.Host == "" || !slices.Contains([]string{"http", "https", "socks5"}, u.Scheme) {
			return errors.New("the proxy must be a URL such as http://proxy.example.com:3128 (http, https or socks5)")
		}
		if u.User != nil {
			return errors.New("put the proxy's username and password in their own fields, not in its URL")
		}
	}
	if s.Proxy == "" && (s.ProxyUser != "" || s.ProxyPassword != "") {
		return errors.New("a proxy username or password needs a proxy URL")
	}
	slices.Sort(s.Channels)
	s.Channels = slices.Compact(s.Channels)
	if s.Channels == nil {
		s.Channels = []int64{}
	}
	return nil
}

// LoadSettings reads the settings, with their defaults.
func LoadSettings(ctx context.Context, st *store.Store) (Settings, error) {
	s := DefaultSettings()
	_, err := st.GetSetting(ctx, SettingKey, &s)
	if s.Channels == nil {
		s.Channels = []int64{}
	}
	return s, err
}

// Release is a release of Eyrie on GitHub.
type Release struct {
	Version   string    `json:"version"` // its tag, such as v1.2.0
	Name      string    `json:"name"`
	Notes     string    `json:"notes"` // Markdown
	URL       string    `json:"url"`
	Published time.Time `json:"published"`
}

// Status is the last check's result.
type Status struct {
	Checked  time.Time `json:"checked"` // zero: never checked
	Latest   *Release  `json:"latest"`  // nil until a check works
	Error    string    `json:"error"`   // why the last check failed
	Notified string    `json:"notified"`
}

// Upgrade is the last time Eyrie started on a newer version than the one
// that ran before.
type Upgrade struct {
	From string    `json:"from"`
	To   string    `json:"to"`
	At   time.Time `json:"at"`
	// Releases are those after From up to To, newest first, once Fetched.
	Releases []Release `json:"releases"`
	Fetched  bool      `json:"fetched"`
}

// Sealer opens the sealed proxy password.
type Sealer interface {
	Open(sealed string) (string, error)
}

// Checker checks for updates. Set its fields, then call Run.
type Checker struct {
	Store   *store.Store
	Secrets Sealer
	Log     *slog.Logger
	Version string // the running version
	Repo    string // owner/name on GitHub; "" cannot check (see RepoFrom)
	// API is the GitHub API; "" is https://api.github.com.
	API string
	// Notify sends a newer release to the channels; nil sends nothing.
	Notify func(channels []int64, current string, r Release)
	// Transport is for tests; nil makes one with the proxy settings.
	Transport http.RoundTripper
	Now       func() time.Time

	mu     sync.Mutex
	status Status
	loaded bool
	tried  time.Time // when the upgrade's notes were last fetched
}

// RepoFrom returns owner/name for a source URL on GitHub, such as the
// image's https://github.com/owner/name, and "" for any other.
func RepoFrom(source string) string {
	u, err := url.Parse(source)
	if err != nil || u.Host != "github.com" {
		return ""
	}
	parts := strings.Split(strings.TrimSuffix(strings.Trim(u.Path, "/"), ".git"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Status returns the last check's result.
func (c *Checker) Status(ctx context.Context) Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.loaded {
		if _, err := c.Store.GetSetting(ctx, statusKey, &c.status); err != nil {
			c.Log.Warn("cannot read the last update check", "err", err)
		}
		c.loaded = true
	}
	return c.status
}

// Available returns the latest release when it is newer than the running
// version, or nil.
func (c *Checker) Available(ctx context.Context) *Release {
	st := c.Status(ctx)
	if st.Latest == nil || !Newer(st.Latest.Version, c.Version) {
		return nil
	}
	return st.Latest
}

// Started keeps the running version and, when it is newer than the one
// that ran before, records the upgrade. Call it once at start, before Run.
// A first start, a downgrade or a development build records none.
func (c *Checker) Started(ctx context.Context) error {
	var prev string
	if _, err := c.Store.GetSetting(ctx, versionKey, &prev); err != nil || prev == c.Version {
		return err
	}
	if Newer(c.Version, prev) {
		c.Log.Info("Eyrie was updated", "from", prev, "to", c.Version)
		if err := c.Store.SetSetting(ctx, upgradeKey, Upgrade{From: prev, To: c.Version, At: c.now(), Releases: []Release{}}, c.now()); err != nil {
			return err
		}
	}
	return c.Store.SetSetting(ctx, versionKey, c.Version, c.now())
}

// Upgrade returns the last upgrade, when it was to the running version.
func (c *Checker) Upgrade(ctx context.Context) (*Upgrade, error) {
	var u Upgrade
	found, err := c.Store.GetSetting(ctx, upgradeKey, &u)
	if err != nil || !found || u.To != c.Version {
		return nil, err
	}
	if u.Releases == nil {
		u.Releases = []Release{}
	}
	return &u, nil
}

// Run checks when a check is due, and fetches the notes of an upgrade's
// releases, while checks are on, until ctx ends.
func (c *Checker) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		if s, err := LoadSettings(ctx, c.Store); err != nil {
			c.Log.Error("cannot read the update settings", "err", err)
		} else if s.Enabled {
			if !c.now().Before(c.Status(ctx).Checked.Add(time.Duration(s.IntervalHours) * time.Hour)) {
				c.Check(ctx, s)
			}
			c.fetchNotes(ctx, s)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Check asks GitHub for the latest release now, keeps the result and, for a
// newer release not yet sent, notifies the chosen channels.
func (c *Checker) Check(ctx context.Context, s Settings) Status {
	st := c.Status(ctx)
	st.Checked = c.now()
	r, err := c.latest(ctx, s)
	if err != nil {
		st.Error = err.Error()
		c.Log.Warn("cannot check for updates", "err", err)
	} else {
		st.Error, st.Latest = "", &r
		if Newer(r.Version, c.Version) && r.Version != st.Notified {
			c.Log.Info("a newer release of Eyrie is out", "version", r.Version, "running", c.Version)
			if s.Notify && len(s.Channels) > 0 && c.Notify != nil {
				c.Notify(s.Channels, c.Version, r)
				st.Notified = r.Version
			}
		}
	}
	c.mu.Lock()
	c.status = st
	c.mu.Unlock()
	if err := c.Store.SetSetting(ctx, statusKey, st, st.Checked); err != nil {
		c.Log.Warn("cannot save the update check", "err", err)
	}
	return st
}

// fetchNotes fetches the notes of the releases in the upgrade to the
// running version, once, while checks are on, trying again an hour after
// a failure.
func (c *Checker) fetchNotes(ctx context.Context, s Settings) {
	u, err := c.Upgrade(ctx)
	if err != nil || u == nil || u.Fetched || !s.Enabled || c.Repo == "" || c.now().Before(c.tried.Add(retryNotes)) {
		return
	}
	c.tried = c.now()
	var list []ghRelease
	if err := c.get(ctx, s, "/releases?per_page=100", &list, 8<<20); err != nil {
		c.Log.Warn("cannot fetch the release notes", "err", err)
		return
	}
	for _, r := range list {
		if !r.Draft && Newer(r.Tag, u.From) && !Newer(r.Tag, u.To) {
			u.Releases = append(u.Releases, r.release())
		}
	}
	slices.SortFunc(u.Releases, func(a, b Release) int {
		if Newer(a.Version, b.Version) {
			return -1
		}
		if Newer(b.Version, a.Version) {
			return 1
		}
		return 0
	})
	u.Releases = u.Releases[:min(len(u.Releases), maxReleases)]
	u.Fetched = true
	if err := c.Store.SetSetting(ctx, upgradeKey, u, c.now()); err != nil {
		c.Log.Warn("cannot save the release notes", "err", err)
	}
}

// ghRelease is a release in GitHub's answers.
type ghRelease struct {
	Tag       string    `json:"tag_name"`
	Name      string    `json:"name"`
	Body      string    `json:"body"`
	URL       string    `json:"html_url"`
	Published time.Time `json:"published_at"`
	Draft     bool      `json:"draft"`
}

func (r ghRelease) release() Release {
	notes := r.Body
	if len(notes) > 20000 {
		notes = notes[:20000] + "…"
	}
	return Release{Version: r.Tag, Name: r.Name, Notes: notes, URL: r.URL, Published: r.Published}
}

// latest fetches the repository's latest release.
func (c *Checker) latest(ctx context.Context, s Settings) (Release, error) {
	var b ghRelease
	if err := c.get(ctx, s, "/releases/latest", &b, 1<<20); err != nil {
		return Release{}, err
	}
	if b.Tag == "" {
		return Release{}, errors.New("GitHub's answer has no release")
	}
	return b.release(), nil
}

// get reads the JSON answer to GET /repos/<repo><path>, of at most limit
// bytes, into v.
func (c *Checker) get(ctx context.Context, s Settings, path string, v any, limit int64) error {
	if c.Repo == "" {
		return errors.New("this build's source (EYRIE_SOURCE_URL) is not a GitHub repository, so there are no releases to check")
	}
	client, err := c.client(s)
	if err != nil {
		return err
	}
	api := c.API
	if api == "" {
		api = "https://api.github.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"/repos/"+c.Repo+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Eyrie/"+c.Version)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub answered %s", resp.Status)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, limit)).Decode(v); err != nil {
		return fmt.Errorf("cannot read GitHub's answer: %w", err)
	}
	return nil
}

// client is an HTTP client through the proxy in s, or the environment's.
func (c *Checker) client(s Settings) (*http.Client, error) {
	if c.Transport != nil && s.Proxy == "" {
		return &http.Client{Transport: c.Transport, Timeout: 30 * time.Second}, nil
	}
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = http.ProxyFromEnvironment
	if s.Proxy != "" {
		u, err := url.Parse(s.Proxy)
		if err != nil {
			return nil, err
		}
		if s.ProxyUser != "" || s.ProxyPassword != "" {
			pw := ""
			if s.ProxyPassword != "" {
				if pw, err = c.Secrets.Open(s.ProxyPassword); err != nil {
					return nil, fmt.Errorf("cannot open the proxy password: %w", err)
				}
			}
			u.User = url.UserPassword(s.ProxyUser, pw)
		}
		t.Proxy = http.ProxyURL(u)
	}
	return &http.Client{Transport: t, Timeout: 30 * time.Second}, nil
}

// Newer reports whether release version a is newer than b, both such as
// v1.2.3. A version that is not one (a development build) is never newer,
// and nothing is newer than it.
func Newer(a, b string) bool {
	x, okA := parse(a)
	y, okB := parse(b)
	if !okA || !okB {
		return false
	}
	return slices.Compare(x, y) > 0
}

func parse(v string) ([]int, bool) {
	v, _, _ = strings.Cut(strings.TrimPrefix(v, "v"), "-") // pre-releases count as their release
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return nil, false
	}
	out := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}
