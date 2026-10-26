package gitlab

import (
	"context"
	"fmt"
	"strings"
)

type PlanOptions struct {
	Target       string
	SkipOptional bool
}

// BuildPlan reads the host and GitLab's public upgrade data and returns the
// stops to run. Upstream data other than the required stops is best effort:
// the plan records what could not be read instead of failing.
func BuildPlan(ctx context.Context, h Host, up Upstream, opts PlanOptions) (Plan, error) {
	facts, err := ReadFacts(ctx, h, true)
	if err != nil {
		return Plan{}, err
	}
	current, err := ParseVersion(facts.Version)
	if err != nil {
		return Plan{}, err
	}
	required, err := up.RequiredStops(ctx)
	if err != nil {
		return Plan{}, fmt.Errorf("required upgrade stops: %w", err)
	}
	path, err := ComputePath(PathInput{
		Current:      current,
		Edition:      facts.Edition,
		Available:    facts.Available,
		Target:       opts.Target,
		Required:     required,
		SkipOptional: opts.SkipOptional,
	})
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Facts: facts, Path: path}
	if len(path.Stops) == 0 {
		return p, nil
	}

	if facts.BatchedPending > 0 {
		p.Problems = append(p.Problems, fmt.Sprintf("%d batched background migrations still running; wait for 0 before the first stop", facts.BatchedPending))
	}
	if len(path.Missing) > 0 {
		p.Problems = append(p.Problems, fmt.Sprintf("not offered by the host's apt repository: %s; do not skip a stop", strings.Join(path.Missing, ", ")))
	}
	if facts.RepoMismatch() {
		p.Problems = append(p.Problems, fmt.Sprintf("apt repository suite %q does not match the OS codename %q; `finish --fix-repo` switches it", facts.RepoSuite, facts.Codename))
	}

	if p.Alerts, err = up.Alerts(ctx, current, path.Target); err != nil {
		p.Problems = append(p.Problems, "alerts.yml not read: "+err.Error())
	}
	if p.Migs, err = up.BackgroundMigrations(ctx); err != nil {
		p.Problems = append(p.Problems, "background_migrations.js not read: "+err.Error())
	}
	for major := current.Major; major <= path.Target.Major; major++ {
		md, err := up.ReleaseNotes(ctx, major)
		if err != nil {
			p.Problems = append(p.Problems, fmt.Sprintf("release notes for %d not read: %v", major, err))
			continue
		}
		notes := ParseUpgradeNotes(md, major, current, path.Target)
		for i := range notes {
			notes[i].Classify(facts)
		}
		p.Notes = append(p.Notes, notes...)
	}
	return p, nil
}

// CheckLine is the one-line summary of `vpd gitlab upgrade check`.
func CheckLine(host string, p Plan) string {
	f := p.Facts
	if len(p.Path.Stops) == 0 {
		return fmt.Sprintf("%s: %s up to date (newest in apt %s)", host, f.Package, p.Path.NewestInApt)
	}
	var stops []string
	for _, s := range p.Path.Stops {
		v := s.Version
		if s.Kind == KindOptional {
			v += " (optional)"
		}
		stops = append(stops, v)
	}
	line := fmt.Sprintf("%s: %s -> %s, %d stop(s): %s", host, f.Package, p.Path.Target, len(stops), strings.Join(stops, ", "))
	if f.RepoMismatch() {
		line += fmt.Sprintf("; apt repo suite %s on %s", f.RepoSuite, f.Codename)
	}
	if f.RebootRequired {
		line += "; reboot pending"
	}
	return line
}

func (p Plan) Markdown(host string) string {
	f := p.Facts
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	w("# GitLab upgrade plan: %s, %s -> %s (%s)\n", host, f.Version, p.Path.Target, strings.ToUpper(f.Edition))
	w("## Versions\n")
	w("- running: `%s`", f.Package)
	w("- newest in apt: `%s`", p.Path.NewestInApt)
	w("- target: %s (%s)", p.Path.Target, p.Path.TargetReason)
	w("")
	w("## Host\n")
	w("- os: %s, kernel %s", f.OS, f.Kernel)
	w("- apt repo suite: %s", f.RepoSuite)
	w("- postgresql: %s", f.Postgres)
	w("- batched migrations pending: %d", f.BatchedPending)
	w("- disk: / %s, /var/opt/gitlab %s", f.DiskRoot, f.DiskData)
	w("- last backup: %s", f.LastBackup)
	w("- features: KAS agents %d, Geo %t, LDAP %t, OmniAuth %t", f.KASAgents, f.Geo, f.LDAP, f.OmniAuth)
	w("- services: %s", strings.Join(f.Services, ", "))
	w("")
	if len(p.Problems) > 0 || len(p.Path.Warnings) > 0 {
		w("## Problems\n")
		for _, s := range append(append([]string{}, p.Problems...), p.Path.Warnings...) {
			w("- **%s**", s)
		}
		w("")
	}
	if len(p.Path.Stops) == 0 {
		w("Nothing to do: the host runs the target already.")
		return b.String()
	}
	w("## Stops\n")
	w("Each stop: backup, `apt-get install %s=<package>` in tmux, wait for background migrations, smoke test.\n", f.PackageName)
	w("| # | Stop | Package | Kind | Migrations | Note |")
	w("|---|---|---|---|---|---|")
	for i, s := range p.Path.Stops {
		v, _ := ParseVersion(s.Version)
		mig := ""
		if c, ok := p.Migs[v.MinorString()]; ok && c.Total > 0 {
			mig = fmt.Sprintf("%d", c.Total)
			if c.Large > 0 {
				mig += fmt.Sprintf(" (%d on large tables)", c.Large)
			}
		}
		pkg := s.Package
		if pkg == "" {
			pkg = "NOT IN APT"
		}
		w("| %d | %s | `%s` | %s | %s | %s |", i+1, s.Version, pkg, s.Kind, mig, s.Note)
	}
	w("")
	if len(p.Notes) > 0 {
		w("## Upgrade notes\n")
		w("| Version | Note | Applies |")
		w("|---|---|---|")
		for _, n := range p.Notes {
			w("| %s | [%s](%s) | %s |", n.Version, n.Title, n.URL, n.Applies)
		}
		w("")
	}
	if len(p.Alerts) > 0 {
		w("## Upgrade-path alerts\n")
		for _, a := range p.Alerts {
			w("- **%s** (%s to %s): %s. %s", strings.ToUpper(a.Severity), a.Start, a.End, a.Title, a.Message)
		}
		w("")
	}
	return b.String()
}
