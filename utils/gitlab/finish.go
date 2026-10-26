package gitlab

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

type FinishOptions struct {
	Reboot        bool
	KeepDBBackups bool
	FixRepo       bool
	SkipRedis     bool
}

// A full backup carries repositories/ and uploads.tar.gz; the package's own
// pre-upgrade backup carries only db/. Full backups stay for the normal
// backup_keep_time rotation.
const cleanupScript = `
for f in /var/opt/gitlab/backups/*_gitlab_backup.tar; do
  [ -f "$f" ] || continue
  if ! tar -tf "$f" 2>/dev/null | grep -qE "^(repositories/|uploads\.tar\.gz)"; then
    echo "$(basename "$f") ($(du -h "$f" | cut -f1))"
    rm -f "$f"
  fi
done
`

// The GitLab package is held during the OS upgrade: an unpinned upgrade
// would jump GitLab past the version the plan ended on.
const osUpgradeScript = `
set -e
apt-get update -qq
apt-mark hold %[1]s >/dev/null
trap 'apt-mark unhold %[1]s >/dev/null' EXIT
DEBIAN_FRONTEND=noninteractive apt-get upgrade -y -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold | grep -E '^Unpacking ' || true
DEBIAN_FRONTEND=noninteractive apt-get autoremove -y -qq >/dev/null 2>&1 || true
`

const fixRepoScript = `
set -e
f=%[1]s; old=%[2]s; new=%[3]s
tmp=$(mktemp)
sed "s/\b$old\b/$new/g" "$f" > "$tmp"
line=$(grep -E '^deb ' "$tmp" | head -1 | sed -E 's/\[[^]]*\] //')
url=$(echo "$line" | awk '{print $2}'); suite=$(echo "$line" | awk '{print $3}')
if ! curl -fsS -o /dev/null --max-time 20 "$url/dists/$suite/InRelease"; then
  rm -f "$tmp"; echo "suite $suite not published at $url" >&2; exit 1
fi
cp -a "$f" "/root/$(basename "$f").$old.bak"
cat "$tmp" > "$f"; rm -f "$tmp"
apt-get update -qq
apt-cache policy %[4]s | awk '/Candidate:/{print $2}'
`

var unpackRe = regexp.MustCompile(`^Unpacking (\S+) \((\S+)\) over \((\S+)\)`)

type Finisher struct {
	Host     Host
	R        *Reporter
	State    *State
	StateDir string
	Opts     FinishOptions
}

func (fn *Finisher) Run(ctx context.Context) error {
	r := fn.R
	fs := &FinishState{Start: time.Now()}
	fn.State.Finish = fs
	defer func() {
		fs.End = time.Now()
		fs.Warnings = r.Warnings()
		if err := fn.State.Save(fn.StateDir); err != nil {
			r.Warn("state not saved: %v", err)
		}
	}()

	f, err := ReadFacts(ctx, fn.Host, false)
	if err != nil {
		return abortAfter("%v", err)
	}
	fs.KernelBefore = f.Kernel
	r.Phase("FINISH on %s: GitLab %s (%s)", fn.State.Host, f.Version, f.PackageName)

	if !fn.Opts.KeepDBBackups {
		r.Phase("FINISH: removing database-only backups written by the package upgrade")
		out, err := fn.Host.Run(ctx, cleanupScript)
		if err != nil {
			r.Warn("backup cleanup: %v", err)
		}
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			if l != "" {
				fs.RemovedBackups = append(fs.RemovedBackups, l)
				r.Info("removed %s", l)
			}
		}
		if len(fs.RemovedBackups) == 0 {
			r.Info("none found")
		}
		if !f.BackupKeepTime {
			r.Warn("backup_keep_time is not set in gitlab.rb: full backups never rotate")
		}
	}

	if f.RepoMismatch() {
		if fn.Opts.FixRepo {
			r.Phase("FINISH: switching apt repository suite %s -> %s", f.RepoSuite, f.Codename)
			out, err := fn.Host.Run(ctx, fmt.Sprintf(fixRepoScript, shellQuote(f.RepoFile), f.RepoSuite, f.Codename, f.PackageName))
			if err != nil {
				r.Warn("repository not switched: %v", err)
			} else {
				fs.RepoFixed = fmt.Sprintf("%s -> %s", f.RepoSuite, f.Codename)
				r.Info("switched; candidate %s, old file kept as /root/<file>.%s.bak", strings.TrimSpace(out), f.RepoSuite)
			}
		} else {
			r.Warn("apt repository suite %s does not match the OS codename %s; run again with --fix-repo", f.RepoSuite, f.Codename)
		}
	}

	r.Phase("FINISH: OS packages, %s on hold", f.PackageName)
	out, err := fn.Host.Run(ctx, fmt.Sprintf(osUpgradeScript, f.PackageName))
	if err != nil {
		r.Warn("OS upgrade: %v", err)
	}
	for _, l := range strings.Split(out, "\n") {
		if m := unpackRe.FindStringSubmatch(l); m != nil {
			fs.Upgraded = append(fs.Upgraded, fmt.Sprintf("%s %s -> %s", m[1], m[3], m[2]))
		}
	}
	r.Info("%d packages upgraded", len(fs.Upgraded))
	for _, u := range fs.Upgraded {
		r.Info("  %s", u)
	}

	f, err = ReadFacts(ctx, fn.Host, false)
	if err != nil {
		return abortAfter("%v", err)
	}
	fs.RebootPending = f.RebootRequired
	switch {
	case f.RebootRequired && fn.Opts.Reboot:
		if err := fn.reboot(ctx); err != nil {
			return err
		}
		fs.Rebooted, fs.RebootPending = true, false
	case f.RebootRequired:
		pkgs, _ := fn.Host.Run(ctx, "cat /var/run/reboot-required.pkgs 2>/dev/null | sort -u | paste -sd, -")
		r.Warn("reboot required (%s); run again with --reboot or reboot by hand in the window", strings.TrimSpace(pkgs))
		fallthrough
	default:
		if !fn.Opts.SkipRedis {
			restarted, err := fn.restartStaleRedis(ctx, f.PackageName)
			if err != nil {
				r.Warn("redis: %v", err)
			}
			fs.RedisRestarted = restarted
		}
	}

	r.Phase("FINISH: final checks")
	if err := Smoke(ctx, fn.Host, r, 18, 10*time.Second); err != nil {
		return err
	}
	holds, _ := fn.Host.Run(ctx, "apt-mark showhold")
	if h := strings.TrimSpace(holds); h != "" {
		r.Warn("packages on hold: %s", strings.ReplaceAll(h, "\n", ", "))
	}
	if c, _ := fn.Host.Run(ctx, "command -v docker >/dev/null && docker ps --format '{{.Names}} {{.Status}}' | paste -sd ';' - || true"); strings.TrimSpace(c) != "" {
		r.Info("containers: %s", strings.TrimSpace(c))
	}
	k, _ := fn.Host.Run(ctx, "uname -r")
	fs.KernelAfter = strings.TrimSpace(k)
	r.Phase("FINISH: done. GitLab %s, kernel %s", f.Version, fs.KernelAfter)
	if len(r.Warnings()) > 0 {
		return &Abort{Code: ExitWarnings, Msg: "finished, with warnings"}
	}
	return nil
}

// reboot tells a real reboot from an SSH session that still reached the host
// while GitLab was shutting down by comparing the boot id.
func (fn *Finisher) reboot(ctx context.Context) error {
	r := fn.R
	before, err := fn.Host.Run(ctx, "cat /proc/sys/kernel/random/boot_id")
	if err != nil {
		return abortAfter("read boot id: %v", err)
	}
	r.Phase("FINISH: rebooting %s", fn.State.Host)
	_, _ = fn.Host.Run(ctx, "systemctl reboot")
	start := time.Now()
	for {
		if time.Since(start) > 10*time.Minute {
			return abortAfter("no reboot observed within 10 minutes")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Second):
		}
		now, err := fn.Host.Run(ctx, "cat /proc/sys/kernel/random/boot_id")
		if err == nil && strings.TrimSpace(now) != "" && now != before {
			r.Info("host is back with a new boot id after %s", time.Since(start).Round(time.Second))
			break
		}
	}
	for i := 0; ; i++ {
		status, _ := fn.Host.Run(ctx, "gitlab-ctl status")
		down := 0
		for _, l := range strings.Split(strings.TrimSpace(status), "\n") {
			if !strings.HasPrefix(l, "run:") {
				down++
			}
		}
		if status != "" && down == 0 {
			r.Info("all services running")
			return nil
		}
		if i == 30 {
			return abortAfter("services not all running 5 minutes after reboot")
		}
		time.Sleep(10 * time.Second)
	}
}

// restartStaleRedis restarts Redis when it started before the GitLab package
// was installed. `gitlab-ctl reconfigure` restarts everything but Redis, so
// the old binary keeps running across upgrades. Puma and Sidekiq reconnect
// on their own.
func (fn *Finisher) restartStaleRedis(ctx context.Context, pkgName string) (bool, error) {
	r := fn.R
	out, err := fn.Host.Run(ctx, fmt.Sprintf(`
up=$(gitlab-ctl status redis | sed -nE 's/^run: redis: \(pid [0-9]+\) ([0-9]+)s.*/\1/p')
installed=$(stat -c %%Y /var/lib/dpkg/info/%s.list)
echo "$up $(( $(date +%%s) - installed ))"`, pkgName))
	if err != nil {
		return false, err
	}
	f := strings.Fields(out)
	if len(f) != 2 {
		return false, fmt.Errorf("redis uptime not readable: %q", strings.TrimSpace(out))
	}
	up, sinceInstall := atoi(f[0]), atoi(f[1])
	if up <= sinceInstall {
		r.Info("redis started after the package install (up %ds); no restart", up)
		return false, nil
	}
	r.Phase("FINISH: restarting redis (up %s, older than the package install)", (time.Duration(up) * time.Second).String())
	if _, err := fn.Host.Run(ctx, "gitlab-ctl restart redis"); err != nil {
		return false, err
	}
	time.Sleep(20 * time.Second)
	errs, _ := fn.Host.Run(ctx, `tail -n 300 /var/log/gitlab/sidekiq/current | grep -ciE 'CannotConnectError|Error connecting to Redis|ECONNREFUSED' || true`)
	if n := atoi(errs); n > 0 {
		r.Warn("%d redis connection errors in the last 300 sidekiq log lines", n)
	} else {
		r.Info("redis restarted; sidekiq log clean")
	}
	return true, nil
}
