package gitlab

import (
	"fmt"
	"regexp"
	"strings"
)

// UpgradeNote is one item of the "Upgrade notes reference" list in
// doc/update/versions/gitlab_<major>_changes.md.
type UpgradeNote struct {
	Version          string   `json:"version"`
	Title            string   `json:"title"`
	URL              string   `json:"url"`
	Scope            []string `json:"scope,omitempty"`
	Affects          string   `json:"affects,omitempty"`
	AffectedVersions string   `json:"affected_versions,omitempty"`
	// Applies is "no: <reason>" when the instance facts rule the note out,
	// otherwise "check".
	Applies string `json:"applies"`
}

var (
	refItemRe = regexp.MustCompile(`^- \[(\d+\.\d+\.\d+)\] - \[(.+?)\]\(#([^)]+)\)\s*(?:\(([^)]*)\))?`)
	affectsRe = regexp.MustCompile(`^- Affects:\s*(.+)$`)
	affVerRe  = regexp.MustCompile(`^- Affected versions:\s*(.+)$`)
)

// ParseUpgradeNotes returns the notes whose version lies in (from, to].
func ParseUpgradeNotes(md string, major int, from, to Version) []UpgradeNote {
	lines := strings.Split(md, "\n")
	var notes []UpgradeNote
	inRef := false
	for _, l := range lines {
		if strings.HasPrefix(l, "## ") {
			inRef = strings.TrimSpace(l) == "## Upgrade notes reference"
			continue
		}
		if !inRef {
			continue
		}
		m := refItemRe.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		v, err := ParseVersion(m[1])
		if err != nil || !from.Less(v) || to.Less(v) {
			continue
		}
		n := UpgradeNote{
			Version: m[1],
			Title:   m[2],
			URL:     fmt.Sprintf("https://docs.gitlab.com/update/versions/gitlab_%d_changes/#%s", major, m[3]),
		}
		for _, s := range strings.Split(m[4], ",") {
			if s = strings.TrimSpace(s); s != "" {
				n.Scope = append(n.Scope, s)
			}
		}
		n.Affects, n.AffectedVersions = sectionMeta(lines, m[2])
		notes = append(notes, n)
	}
	return notes
}

// sectionMeta reads the Affects and Affected versions bullets under the
// "### <title>" heading of the notes section.
func sectionMeta(lines []string, title string) (affects, versions string) {
	in := false
	for _, l := range lines {
		if strings.HasPrefix(l, "### ") {
			if in {
				return
			}
			in = strings.TrimSpace(strings.TrimPrefix(l, "### ")) == title
			continue
		}
		if !in {
			continue
		}
		if m := affectsRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			affects = m[1]
		}
		if m := affVerRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			versions = m[1]
		}
	}
	return
}

// Classify rules out notes that cannot touch a single-node Linux package
// install with the given facts. Everything else stays "check": deciding
// whether a note matters is left to whoever reads the plan.
func (n *UpgradeNote) Classify(f Facts) {
	where := strings.ToLower(strings.Join(n.Scope, ",") + " " + n.Affects)
	linux := strings.Contains(where, "linux package") || strings.Contains(where, "all installation methods")
	switch {
	case strings.TrimSpace(where) != "" && !linux && !strings.Contains(where, "geo"):
		n.Applies = "no: not the Linux package (" + strings.TrimSpace(n.Affects+" "+strings.Join(n.Scope, ", ")) + ")"
	case (strings.Contains(where, "geo") || strings.Contains(strings.ToLower(n.Title), "geo")) && !f.Geo:
		n.Applies = "no: Geo is not configured"
	default:
		n.Applies = "check"
	}
}
