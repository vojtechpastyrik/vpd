package gitlab

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// RequiredStop is one entry of GitLab's config/upgrade_path.yml.
type RequiredStop struct {
	Major    int    `yaml:"major"`
	Minor    int    `yaml:"minor"`
	Comments string `yaml:"comments"`
}

func ParseUpgradePath(data []byte) ([]RequiredStop, error) {
	var stops []RequiredStop
	if err := yaml.Unmarshal(data, &stops); err != nil {
		return nil, fmt.Errorf("parse upgrade_path.yml: %w", err)
	}
	return stops, nil
}

// PlanStop is one apt install of the plan.
type PlanStop struct {
	Version     string `json:"version"`
	Package     string `json:"package"`
	Kind        string `json:"kind"`
	Note        string `json:"note,omitempty"`
	Conditional bool   `json:"conditional,omitempty"`
}

const (
	KindOptional = "optional"
	KindRequired = "required"
	KindTarget   = "target"
)

type PathInput struct {
	Current   Version
	Edition   string
	Available []string // apt versions, e.g. "19.5.1-ce.0"
	Target    string   // "", "X.Y" or "X.Y.Z"; empty means the newest version apt offers
	Required  []RequiredStop
	// SkipOptional leaves out the latest patch of the running minor, which
	// GitLab's upgrade-path tool recommends but does not require.
	SkipOptional bool
}

type Path struct {
	Target       Version    `json:"target"`
	TargetReason string     `json:"target_reason"`
	NewestInApt  string     `json:"newest_in_apt"`
	Stops        []PlanStop `json:"stops"`
	// Missing lists required minors the host's apt repository does not offer.
	Missing  []string `json:"missing,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// ComputePath keeps the required stops between the running version and the
// target and picks for each the newest patch apt offers. apt is the source of
// truth for patch numbers: GitLab's path.json lags behind published packages.
func ComputePath(in PathInput) (Path, error) {
	pkgs := aptVersions(in.Available, in.Edition)
	p := Path{}
	if len(pkgs) > 0 {
		p.NewestInApt = pkgs[len(pkgs)-1].pkg
	}

	var target Version
	exact := false
	switch {
	case in.Target == "":
		if len(pkgs) == 0 {
			return p, fmt.Errorf("apt offers no gitlab-%s versions", in.Edition)
		}
		target = pkgs[len(pkgs)-1].v
		p.TargetReason = "newest version the host's apt repository offers"
	default:
		t, err := ParseVersion(in.Target)
		if err != nil {
			return p, err
		}
		exact = strings.Count(in.Target, ".") == 2
		if exact {
			target = t
			p.TargetReason = "set with --target"
		} else {
			newest, ok := newestPatch(pkgs, t)
			if !ok {
				return p, fmt.Errorf("apt offers no %s.x version of gitlab-%s", t.MinorString(), in.Edition)
			}
			target = newest.v
			p.TargetReason = fmt.Sprintf("newest %s patch the host's apt repository offers", t.MinorString())
		}
	}
	p.Target = target

	if !in.Current.Less(target) {
		return p, nil
	}
	if target.Patch == 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("target %s is a .0 release; the first patch usually follows within days", target))
	}

	for _, s := range in.Required {
		sv := Version{Major: s.Major, Minor: s.Minor}
		if !in.Current.MinorLess(sv) || !sv.MinorLess(target) {
			continue
		}
		stop := PlanStop{
			Version:     sv.MinorString(),
			Kind:        KindRequired,
			Note:        cleanComment(s.Comments),
			Conditional: strings.Contains(strings.ToLower(s.Comments), "conditional"),
		}
		if newest, ok := newestPatch(pkgs, sv); ok {
			stop.Version, stop.Package = newest.v.String(), newest.pkg
		} else if !stop.Conditional {
			p.Missing = append(p.Missing, sv.MinorString())
		}
		p.Stops = append(p.Stops, stop)
	}

	targetStop := PlanStop{Version: target.String(), Kind: KindTarget}
	if exact {
		for _, a := range pkgs {
			if a.v == target {
				targetStop.Package = a.pkg
			}
		}
	} else if newest, ok := newestPatch(pkgs, target); ok {
		targetStop.Package = newest.pkg
	}
	if targetStop.Package == "" {
		p.Missing = append(p.Missing, target.String())
	}
	p.Stops = append(p.Stops, targetStop)

	if !in.SkipOptional && !p.Stops[0].isMinor(in.Current) {
		if newest, ok := newestPatch(pkgs, in.Current); ok && in.Current.Less(newest.v) {
			opt := PlanStop{
				Version: newest.v.String(),
				Package: newest.pkg,
				Kind:    KindOptional,
				Note:    "latest patch of the running minor; carries upgrade-path fixes",
			}
			p.Stops = append([]PlanStop{opt}, p.Stops...)
		}
	}
	return p, nil
}

func (s PlanStop) isMinor(v Version) bool {
	sv, err := ParseVersion(s.Version)
	return err == nil && sv.SameMinor(v)
}

type aptVersion struct {
	v   Version
	pkg string
}

func aptVersions(available []string, edition string) []aptVersion {
	re := regexp.MustCompile(`^(\d+\.\d+\.\d+)-` + regexp.QuoteMeta(edition) + `\.\d+$`)
	var out []aptVersion
	for _, a := range available {
		a = strings.TrimSpace(a)
		m := re.FindStringSubmatch(a)
		if m == nil {
			continue
		}
		v, err := ParseVersion(m[1])
		if err != nil {
			continue
		}
		out = append(out, aptVersion{v: v, pkg: a})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].v.Less(out[j].v) })
	return out
}

func newestPatch(pkgs []aptVersion, minor Version) (aptVersion, bool) {
	var best aptVersion
	found := false
	for _, a := range pkgs {
		if a.v.SameMinor(minor) {
			best, found = a, true
		}
	}
	return best, found
}

func cleanComment(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
