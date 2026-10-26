package gitlab

import (
	"os"
	"strings"
	"testing"
)

func requiredStops(t *testing.T) []RequiredStop {
	t.Helper()
	data, err := os.ReadFile("testdata/upgrade_path.yml")
	if err != nil {
		t.Fatal(err)
	}
	stops, err := ParseUpgradePath(data)
	if err != nil {
		t.Fatal(err)
	}
	return stops
}

func ce(versions ...string) []string {
	out := make([]string, len(versions))
	for i, v := range versions {
		out[i] = v + "-ce.0"
	}
	return out
}

func stopVersions(p Path) string {
	var s []string
	for _, st := range p.Stops {
		s = append(s, st.Version+":"+st.Kind)
	}
	return strings.Join(s, " ")
}

func mustVersion(t *testing.T, s string) Version {
	t.Helper()
	v, err := ParseVersion(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestComputePath(t *testing.T) {
	required := requiredStops(t)
	avail := ce("18.9.2", "18.9.5", "18.11.3", "18.11.4", "19.0.1", "19.2.2", "19.2.4", "19.4.1", "19.4.2", "19.4.3", "19.5.0", "19.5.1")

	tests := []struct {
		name, current, target string
		skipOptional          bool
		available             []string
		want                  string
		wantMissing           string
		wantWarning           bool
	}{
		{name: "default target is newest in apt, optional patch first", current: "19.4.2",
			want: "19.4.3:optional 19.5.1:target"},
		{name: "skip optional patch", current: "19.4.2", skipOptional: true,
			want: "19.5.1:target"},
		{name: "required stops between", current: "18.9.2", target: "19.5",
			want: "18.9.5:optional 18.11.4:required 19.2.4:required 19.5.1:target"},
		{name: "exact target", current: "19.4.3", target: "19.5.0",
			want: "19.5.0:target", wantWarning: true},
		{name: "same minor patch has no optional stop", current: "19.5.0",
			want: "19.5.1:target"},
		{name: "up to date", current: "19.5.1", want: ""},
		{name: "required minor missing in apt", current: "18.9.5", target: "19.5",
			available: ce("18.9.5", "19.5.1"), want: "18.11:required 19.2:required 19.5.1:target", wantMissing: "18.11 19.2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := avail
			if tt.available != nil {
				a = tt.available
			}
			p, err := ComputePath(PathInput{
				Current: mustVersion(t, tt.current), Edition: "ce", Available: a,
				Target: tt.target, Required: required, SkipOptional: tt.skipOptional,
			})
			if err != nil {
				t.Fatal(err)
			}
			if got := stopVersions(p); got != tt.want {
				t.Errorf("stops = %q, want %q", got, tt.want)
			}
			if got := strings.Join(p.Missing, " "); got != tt.wantMissing {
				t.Errorf("missing = %q, want %q", got, tt.wantMissing)
			}
			if got := len(p.Warnings) > 0; got != tt.wantWarning {
				t.Errorf("warnings = %v, want any: %v", p.Warnings, tt.wantWarning)
			}
		})
	}
}

func TestComputePathIgnoresOtherEdition(t *testing.T) {
	p, err := ComputePath(PathInput{
		Current:   mustVersion(t, "19.4.2"),
		Edition:   "ce",
		Available: append(ce("19.5.1"), "19.6.0-ee.0"),
		Required:  requiredStops(t),
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Target.String() != "19.5.1" {
		t.Errorf("target = %s, want 19.5.1", p.Target)
	}
}
