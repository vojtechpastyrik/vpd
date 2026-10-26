package gitlab

import (
	"fmt"
	"regexp"
	"strconv"
)

type Version struct {
	Major, Minor, Patch int
}

var versionRe = regexp.MustCompile(`^(\d+)\.(\d+)(?:\.(\d+))?`)

// ParseVersion reads "19.5", "19.5.1", "19.5.1-ce" and "19.5.1-ce.0".
func ParseVersion(s string) (Version, error) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("not a GitLab version: %q", s)
	}
	v := Version{}
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	if m[3] != "" {
		v.Patch, _ = strconv.Atoi(m[3])
	}
	return v, nil
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

func (v Version) MinorString() string {
	return fmt.Sprintf("%d.%d", v.Major, v.Minor)
}

func (v Version) Compare(o Version) int {
	switch {
	case v.Major != o.Major:
		return cmpInt(v.Major, o.Major)
	case v.Minor != o.Minor:
		return cmpInt(v.Minor, o.Minor)
	default:
		return cmpInt(v.Patch, o.Patch)
	}
}

func (v Version) Less(o Version) bool { return v.Compare(o) < 0 }

func (v Version) SameMinor(o Version) bool {
	return v.Major == o.Major && v.Minor == o.Minor
}

func (v Version) MinorLess(o Version) bool {
	return v.Major < o.Major || (v.Major == o.Major && v.Minor < o.Minor)
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
