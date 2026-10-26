package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	upgradePathURL       = "https://gitlab.com/gitlab-org/gitlab/-/raw/master/config/upgrade_path.yml"
	upgradePathMirrorURL = "https://gitlab.com/gitlab-com/support/toolbox/upgrade-path/-/raw/main/upgrade-path.yml"
	alertsURL            = "https://gitlab.com/gitlab-com/support/toolbox/upgrade-path/-/raw/main/alerts.yml"
	backgroundMigURL     = "https://gitlab.com/gitlab-com/support/toolbox/upgrade-path/-/raw/main/ruby/background_migrations.js"
	releaseNotesURL      = "https://gitlab.com/gitlab-org/gitlab/-/raw/master/doc/update/versions/gitlab_%d_changes.md"
)

// Fetcher downloads one public URL.
type Fetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

// Upstream reads the public data GitLab's upgrade-path tool renders.
type Upstream struct {
	Fetcher Fetcher
}

func (u Upstream) RequiredStops(ctx context.Context) ([]RequiredStop, error) {
	data, err := u.Fetcher.Fetch(ctx, upgradePathURL)
	if err != nil {
		if data, err = u.Fetcher.Fetch(ctx, upgradePathMirrorURL); err != nil {
			return nil, err
		}
	}
	return ParseUpgradePath(data)
}

type Alert struct {
	Start     string `yaml:"start" json:"start"`
	End       string `yaml:"end" json:"end"`
	Severity  string `yaml:"severity" json:"severity"`
	Title     string `yaml:"title" json:"title"`
	CheckWhen string `yaml:"check_when" json:"check_when"`
	Message   string `yaml:"message" json:"message"`
}

// Alerts returns the upgrade-path tool's warnings whose version range
// overlaps [from, to].
func (u Upstream) Alerts(ctx context.Context, from, to Version) ([]Alert, error) {
	data, err := u.Fetcher.Fetch(ctx, alertsURL)
	if err != nil {
		return nil, err
	}
	var all []Alert
	if err := yaml.Unmarshal(data, &all); err != nil {
		return nil, fmt.Errorf("parse alerts.yml: %w", err)
	}
	var out []Alert
	for _, a := range all {
		start, err1 := ParseVersion(a.Start)
		end, err2 := ParseVersion(a.End)
		if err1 != nil || err2 != nil {
			continue
		}
		if !to.Less(start) && !end.Less(from) {
			a.Message = cleanComment(a.Message)
			out = append(out, a)
		}
	}
	return out, nil
}

type MigrationCount struct {
	Total int `json:"total"`
	Large int `json:"large_tables"`
}

// BackgroundMigrations counts the batched background migrations each minor
// queues, keyed by "X.Y".
func (u Upstream) BackgroundMigrations(ctx context.Context) (map[string]MigrationCount, error) {
	data, err := u.Fetcher.Fetch(ctx, backgroundMigURL)
	if err != nil {
		return nil, err
	}
	return ParseBackgroundMigrations(data)
}

func ParseBackgroundMigrations(js []byte) (map[string]MigrationCount, error) {
	s := string(js)
	start, end := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return nil, errors.New("background_migrations.js: no object found")
	}
	var raw map[string][]struct {
		HasLargeTable bool `json:"has_large_table"`
	}
	if err := json.Unmarshal([]byte(s[start:end+1]), &raw); err != nil {
		return nil, fmt.Errorf("parse background_migrations.js: %w", err)
	}
	out := map[string]MigrationCount{}
	for minor, list := range raw {
		c := MigrationCount{Total: len(list)}
		for _, m := range list {
			if m.HasLargeTable {
				c.Large++
			}
		}
		out[minor] = c
	}
	return out, nil
}

func (u Upstream) ReleaseNotes(ctx context.Context, major int) (string, error) {
	data, err := u.Fetcher.Fetch(ctx, fmt.Sprintf(releaseNotesURL, major))
	return string(data), err
}

type HTTPFetcher struct {
	Client *http.Client
}

func (f HTTPFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	client := f.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: HTTP %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// HostFetcher downloads through the GitLab host, for workstations whose
// outbound HTTPS is filtered while the host's is not.
type HostFetcher struct {
	Host Host
}

func (f HostFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	out, err := f.Host.Run(ctx, "curl -fsSL --max-time 30 "+shellQuote(url))
	if err != nil {
		return nil, fmt.Errorf("fetch %s on host: %w", url, err)
	}
	return []byte(out), nil
}

// FallbackFetcher tries each fetcher in order and returns the first success.
type FallbackFetcher []Fetcher

func (fs FallbackFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	var errs []error
	for _, f := range fs {
		data, err := f.Fetch(ctx, url)
		if err == nil {
			return data, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
