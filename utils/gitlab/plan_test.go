package gitlab

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

type fakeHost struct{}

func (fakeHost) Run(_ context.Context, script string) (string, error) {
	switch {
	case strings.Contains(script, "apt-cache madison"):
		return "19.5.1-ce.0\n19.5.0-ce.0\n19.4.3-ce.0\n19.4.2-ce.0\n", nil
	case strings.Contains(script, "v version"):
		return "version=19.4.2\npackage_name=gitlab-ce\npackage=19.4.2-ce.0\ncodename=trixie\nrepo_suite=bookworm\nbatched_pending=0\ngeo=0\nkas_agents=0\n", nil
	}
	return "", nil
}

type fileFetcher map[string]string

func (f fileFetcher) Fetch(_ context.Context, url string) ([]byte, error) {
	for suffix, file := range f {
		if strings.HasSuffix(url, suffix) {
			return os.ReadFile(file)
		}
	}
	return nil, errors.New("not found: " + url)
}

func TestBuildPlan(t *testing.T) {
	up := Upstream{Fetcher: fileFetcher{
		"config/upgrade_path.yml": "testdata/upgrade_path.yml",
		"gitlab_19_changes.md":    "testdata/gitlab_19_changes.md",
	}}
	p, err := BuildPlan(context.Background(), fakeHost{}, up, PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := stopVersions(p.Path); got != "19.4.3:optional 19.5.1:target" {
		t.Errorf("stops = %q", got)
	}
	if len(p.Notes) != 3 {
		t.Errorf("notes = %d, want 3", len(p.Notes))
	}
	md := p.Markdown("example")
	for _, want := range []string{"`19.5.1-ce.0`", "does not match the OS codename", "alerts.yml not read", "| 19.5.0 | [Agent for Kubernetes"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown misses %q:\n%s", want, md)
		}
	}
	if line := CheckLine("example", p); line != "example: 19.4.2-ce.0 -> 19.5.1, 2 stop(s): 19.4.3 (optional), 19.5.1; apt repo suite bookworm on trixie" {
		t.Errorf("check line = %q", line)
	}
}
