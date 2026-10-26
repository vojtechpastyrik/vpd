package gitlab

import (
	"bufio"
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Facts is what the plan and the checks need to know about the host.
type Facts struct {
	Version        string   `json:"version"`
	Edition        string   `json:"edition"`
	PackageName    string   `json:"package_name"`
	Package        string   `json:"package"`
	OS             string   `json:"os"`
	Codename       string   `json:"codename"`
	RepoFile       string   `json:"repo_file,omitempty"`
	RepoSuite      string   `json:"repo_suite,omitempty"`
	Postgres       string   `json:"postgres"`
	BatchedPending int      `json:"batched_pending"`
	DiskRoot       string   `json:"disk_root"`
	DiskData       string   `json:"disk_data"`
	LastBackup     string   `json:"last_backup,omitempty"`
	BackupKeepTime bool     `json:"backup_keep_time_set"`
	ExternalURL    string   `json:"external_url"`
	KASAgents      int      `json:"kas_agents"`
	Geo            bool     `json:"geo"`
	LDAP           bool     `json:"ldap"`
	OmniAuth       bool     `json:"omniauth"`
	Services       []string `json:"services"`
	RebootRequired bool     `json:"reboot_required"`
	Kernel         string   `json:"kernel"`
	Available      []string `json:"available,omitempty"`
}

func (f Facts) Has(service string) bool {
	for _, s := range f.Services {
		if s == service {
			return true
		}
	}
	return false
}

func (f Facts) RepoMismatch() bool {
	return f.RepoSuite != "" && f.Codename != "" && f.RepoSuite != f.Codename
}

const factsScript = `
set -u
v() { printf '%s=%s\n' "$1" "$2"; }
psql() { gitlab-psql -d gitlabhq_production -tAc "$1" 2>/dev/null | tr -d ' '; }
v version "$(cat /opt/gitlab/embedded/service/gitlab-rails/VERSION 2>/dev/null)"
# dpkg -l, not dpkg-query with both names: a package that was never
# installed prints an empty line first and the wrong edition wins.
pkg=$(dpkg -l 2>/dev/null | awk '/^ii  gitlab-(ee|ce) /{print $2" "$3}' | head -1)
v package_name "${pkg%% *}"
v package "${pkg##* }"
. /etc/os-release
v os "$PRETTY_NAME"
v codename "${VERSION_CODENAME:-}"
repo=$(grep -lE '^deb .*packages.gitlab.com/gitlab/gitlab-(ce|ee)' /etc/apt/sources.list.d/*.list 2>/dev/null | head -1)
v repo_file "$repo"
[ -n "$repo" ] && v repo_suite "$(grep -E '^deb ' "$repo" | head -1 | sed -E 's/\[[^]]*\] //' | awk '{print $3}')"
v postgres "$(psql 'SHOW server_version;')"
v batched_pending "$(psql 'SELECT count(*) FROM batched_background_migrations WHERE status NOT IN (3,6);')"
v kas_agents "$(psql 'SELECT count(*) FROM cluster_agents;')"
v geo "$(psql 'SELECT count(*) FROM geo_nodes;')"
v disk_root "$(df -h / | awk 'NR==2{print $4" free of "$2}')"
v disk_data "$(df -h /var/opt/gitlab | awk 'NR==2{print $4" free of "$2}')"
v last_backup "$(ls -t /var/opt/gitlab/backups/*_gitlab_backup.tar 2>/dev/null | head -1 | xargs -r basename)"
v backup_keep_time "$(grep -cE "^gitlab_rails\['backup_keep_time'\]" /etc/gitlab/gitlab.rb)"
v external_url "$(grep -E '^external_url' /etc/gitlab/gitlab.rb | sed -E "s/.*['\"]([^'\"]+)['\"].*/\1/")"
v ldap "$(grep -cE "^gitlab_rails\['ldap_enabled'\]\s*=\s*true" /etc/gitlab/gitlab.rb)"
v omniauth "$(grep -cE "^gitlab_rails\['omniauth_providers'\]" /etc/gitlab/gitlab.rb)"
v services "$(gitlab-ctl status 2>/dev/null | awk -F'[: ]+' '/^run:/{print $2}' | paste -sd, -)"
v reboot_required "$(test -f /var/run/reboot-required && echo 1 || echo 0)"
v kernel "$(uname -r)"
`

const availableScript = `
apt-get update -qq >/dev/null 2>&1 || true
apt-cache madison %s | awk '{print $3}'
`

// ReadFacts reads the host state. withAvailable also refreshes apt and lists
// the GitLab versions the repository offers, which takes a few seconds.
func ReadFacts(ctx context.Context, h Host, withAvailable bool) (Facts, error) {
	out, err := h.Run(ctx, factsScript)
	if err != nil {
		return Facts{}, fmt.Errorf("read host state: %w", err)
	}
	kv := parseKV(out)
	f := Facts{
		Version:        kv["version"],
		PackageName:    kv["package_name"],
		Package:        kv["package"],
		OS:             kv["os"],
		Codename:       kv["codename"],
		RepoFile:       kv["repo_file"],
		RepoSuite:      kv["repo_suite"],
		Postgres:       kv["postgres"],
		DiskRoot:       kv["disk_root"],
		DiskData:       kv["disk_data"],
		LastBackup:     kv["last_backup"],
		BackupKeepTime: atoi(kv["backup_keep_time"]) > 0,
		ExternalURL:    kv["external_url"],
		KASAgents:      atoi(kv["kas_agents"]),
		Geo:            atoi(kv["geo"]) > 0,
		LDAP:           atoi(kv["ldap"]) > 0,
		OmniAuth:       atoi(kv["omniauth"]) > 0,
		RebootRequired: kv["reboot_required"] == "1",
		Kernel:         kv["kernel"],
	}
	f.BatchedPending = atoi(kv["batched_pending"])
	f.Edition = strings.TrimPrefix(f.PackageName, "gitlab-")
	if s := kv["services"]; s != "" {
		f.Services = strings.Split(s, ",")
	}
	if f.Version == "" || f.Edition == "" {
		return f, fmt.Errorf("no Linux package GitLab found on the host")
	}
	if withAvailable {
		avail, err := h.Run(ctx, fmt.Sprintf(availableScript, f.PackageName))
		if err != nil {
			return f, fmt.Errorf("list apt versions: %w", err)
		}
		f.Available = strings.Fields(avail)
	}
	return f, nil
}

func (f Facts) FQDN() string {
	u := strings.TrimPrefix(strings.TrimPrefix(f.ExternalURL, "https://"), "http://")
	return strings.SplitN(u, "/", 2)[0]
}

func parseKV(out string) map[string]string {
	kv := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			kv[k] = strings.TrimSpace(v)
		}
	}
	return kv
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
