package gitlab

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const remoteLogDir = "/var/log/gitlab-upgrade"

// Runner executes the plan's stops one at a time and records progress in the
// state after every phase.
type Runner struct {
	Host     Host
	R        *Reporter
	State    *State
	StateDir string
	// Gate stops after each finished stop that is not the last one, so the
	// operator can push, run CI and pull from the registry before the next.
	Gate               bool
	MigrationsTimeout  time.Duration
	PollInterval       time.Duration
	Heartbeat          time.Duration
	LegacyCheckEvery   int
	HealthAttempts     int
	HealthRetryBackoff time.Duration
}

func (rn *Runner) save() {
	if err := rn.State.Save(rn.StateDir); err != nil {
		rn.R.Warn("state not saved: %v", err)
	}
}

// Run continues with the first stop that is not done. A stop whose package
// is already installed resumes at the migration wait.
func (rn *Runner) Run(ctx context.Context) error {
	for i := range rn.State.Stops {
		st := &rn.State.Stops[i]
		if st.Status == StopDone {
			continue
		}
		if st.Package == "" {
			return abortBefore("stop %s has no package in apt; re-run plan", st.Version)
		}
		seen := len(rn.R.Warnings())
		if err := rn.runStop(ctx, st); err != nil {
			st.Status = StopFailed
			st.Error = err.Error()
			st.end()
			rn.save()
			return err
		}
		st.Status = StopDone
		st.Warnings = rn.R.Warnings()[seen:]
		rn.save()
		if rn.Gate && i < len(rn.State.Stops)-1 {
			rn.R.Phase("STOP %s: done. Gate: verify git push, a CI job and a registry pull, then run again for %s", st.Package, rn.State.Stops[i+1].Package)
			return &Abort{Code: ExitGate, Msg: "waiting for manual checks"}
		}
	}
	if len(rn.R.Warnings()) > 0 {
		return &Abort{Code: ExitWarnings, Msg: "all stops done, with warnings"}
	}
	rn.R.Phase("all stops done; next: `vpd gitlab upgrade finish`")
	return nil
}

func (rn *Runner) runStop(ctx context.Context, st *StopState) error {
	r := rn.R
	target, _ := ParseVersion(st.Version)
	st.Status = StopRunning
	rn.save()

	r.Phase("STOP %s on %s: preflight", st.Package, rn.State.Host)
	st.begin("preflight")
	facts, err := ReadFacts(ctx, rn.Host, false)
	if err != nil {
		return abortBefore("%v", err)
	}
	running, _ := ParseVersion(facts.Version)
	resume := running == target
	switch {
	case resume:
		r.Info("%s already installed; resuming at the migration wait", st.Version)
	case target.Less(running):
		return abortBefore("host runs %s, newer than this stop; re-run plan", running)
	default:
		r.Info("running %s, installing %s", running, st.Package)
		if err := rn.preflight(ctx, facts, st); err != nil {
			return err
		}
	}
	st.end()
	rn.save()

	if !resume {
		r.Phase("STOP %s: backup (gitlab-backup create SKIP=registry), the slow part", st.Package)
		st.begin("backup")
		if err := rn.remoteLogged(ctx, st.Version, "backup", "gitlab-backup create SKIP=registry STRATEGY=copy"); err != nil {
			return abortBefore("backup failed: %v", err)
		}
		out, _ := rn.Host.Run(ctx, "ls -t /var/opt/gitlab/backups/*_gitlab_backup.tar | head -1 | xargs -r basename")
		st.Backup = strings.TrimSpace(out)
		r.Info("backup done: %s", st.Backup)
		st.end()
		rn.save()

		r.Phase("STOP %s: apt-get install %s=%s (reconfigure runs inside)", st.Package, facts.PackageName, st.Package)
		st.begin("install")
		installErr := rn.remoteLogged(ctx, st.Version, "install",
			fmt.Sprintf("DEBIAN_FRONTEND=noninteractive apt-get install -y %s=%s", facts.PackageName, st.Package))
		now, err := ReadFacts(ctx, rn.Host, false)
		switched := err == nil && now.Version != facts.Version
		if installErr != nil {
			if switched {
				return abortAfter("install failed after the package switched to %s: %v; see %s/%s-install.log on the host", now.Version, installErr, remoteLogDir, st.Version)
			}
			return abortBefore("install failed, package not switched: %v; see %s/%s-install.log on the host", installErr, remoteLogDir, st.Version)
		}
		if v, _ := ParseVersion(now.Version); v != target {
			return abortAfter("VERSION says %s after install, expected %s", now.Version, target)
		}
		r.Info("installed, VERSION=%s", now.Version)
		st.end()
		rn.save()
	}

	r.Phase("STOP %s: waiting for background migrations (batched via SQL, legacy via rake)", st.Package)
	st.begin("migrations")
	if err := rn.waitMigrations(ctx, target); err != nil {
		return err
	}
	st.end()
	rn.save()

	r.Phase("STOP %s: smoke test", st.Package)
	st.begin("smoke")
	if err := Smoke(ctx, rn.Host, r, rn.HealthAttempts, rn.HealthRetryBackoff); err != nil {
		return err
	}
	st.end()
	r.Phase("STOP %s: done", st.Package)
	return nil
}

func (rn *Runner) preflight(ctx context.Context, f Facts, st *StopState) error {
	if f.BatchedPending > 0 {
		return abortBefore("%d batched background migrations still running from the previous version", f.BatchedPending)
	}
	out, err := rn.Host.Run(ctx, fmt.Sprintf("apt-cache madison %s | awk '{print $3}' | grep -cx %s || true", f.PackageName, shellQuote(st.Package)))
	if err != nil {
		return abortBefore("apt-cache: %v", err)
	}
	if atoi(out) == 0 {
		return abortBefore("%s=%s is not in the apt repository", f.PackageName, st.Package)
	}
	// Long commands must survive a dropped SSH session; tmux also lets a
	// human attach. Without it, setsid+nohup keeps the survival.
	if _, err := rn.Host.Run(ctx, "command -v tmux >/dev/null || DEBIAN_FRONTEND=noninteractive apt-get install -y -qq tmux >/dev/null 2>&1"); err != nil {
		rn.R.Warn("tmux missing and not installable; falling back to setsid+nohup (no attachable session)")
	}
	rn.R.Info("preflight ok")
	return nil
}

const remoteLoggedScript = `
set -e
dir=%[1]s; name=%[2]s; log="$dir/%[3]s-%[2]s.log"; script="$dir/%[3]s-%[2]s.sh"
mkdir -p "$dir"
cat > "$script" <<'CMD'
%[4]s
CMD
: > "$log"
if command -v tmux >/dev/null; then
  tmux kill-session -t "gu-$name" 2>/dev/null || true
  tmux new-session -d -s "gu-$name" "bash $script >> $log 2>&1; echo __EXIT:\$? >> $log"
else
  setsid nohup bash -c "bash $script >> $log 2>&1; echo __EXIT:\$? >> $log" >/dev/null 2>&1 &
fi
`

// remoteLogged runs cmd on the host detached from this SSH session and waits
// for it. Its output stays in the host log; only a heartbeat with the last
// line is reported, so a long backup does not flood whoever watches.
func (rn *Runner) remoteLogged(ctx context.Context, version, name, cmd string) error {
	if _, err := rn.Host.Run(ctx, fmt.Sprintf(remoteLoggedScript, remoteLogDir, name, version, cmd)); err != nil {
		return err
	}
	log := fmt.Sprintf("%s/%s-%s.log", remoteLogDir, version, name)
	start := time.Now()
	lastBeat := start
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(rn.PollInterval):
		}
		out, err := rn.Host.Run(ctx, fmt.Sprintf("grep -m1 '^__EXIT:' %[1]s || true; echo; grep -v '^__EXIT:' %[1]s | tail -n1", log))
		if err != nil {
			rn.R.Warn("%s: log not readable this round: %v", name, err)
			continue
		}
		exitLine, last, _ := strings.Cut(out, "\n")
		if code, ok := strings.CutPrefix(strings.TrimSpace(exitLine), "__EXIT:"); ok {
			if code != "0" {
				return fmt.Errorf("exit %s, last line: %s", code, truncate(strings.TrimSpace(last), 160))
			}
			return nil
		}
		if time.Since(lastBeat) >= rn.Heartbeat {
			lastBeat = time.Now()
			rn.R.Info("%s running %s; last line: %s", name, time.Since(start).Round(time.Second), truncate(strings.TrimSpace(last), 120))
		}
	}
}

func (rn *Runner) waitMigrations(ctx context.Context, v Version) error {
	start := time.Now()
	legacy, legacyAt := -1, "never"
	last := ""
	stuck := map[string]bool{}
	for tick := 0; ; tick++ {
		batched, err := PendingBatched(ctx, rn.Host)
		if err != nil {
			rn.R.Warn("%v", err)
		}
		if err == nil && (len(batched) == 0 || tick%rn.LegacyCheckEvery == 0) {
			if n, lerr := LegacyNotFinished(ctx, rn.Host, v); lerr == nil {
				legacy, legacyAt = n, time.Now().Format("15:04:05")
			} else {
				rn.R.Warn("%v", lerr)
			}
		}
		if err == nil && len(batched) == 0 && legacy == 0 {
			rn.R.Info("migrations finished after %s", time.Since(start).Round(time.Second))
			return nil
		}
		var names []string
		for _, m := range batched {
			names = append(names, m.String())
		}
		msg := fmt.Sprintf("%d batched pending: %s; legacy not finished: %d (checked %s)", len(batched), strings.Join(names, ", "), legacy, legacyAt)
		if msg != last {
			rn.R.Info("%s", msg)
			last = msg
		}
		for _, m := range batched {
			if (m.MaxAttempts >= 3 || m.OnHoldUntil != "") && !stuck[m.Class] {
				stuck[m.Class] = true
				rn.R.Warn("%s looks stuck: attempts climbing or on hold", m.Class)
			}
		}
		if time.Since(start) >= rn.MigrationsTimeout {
			return abortAfter("migrations not finished after %s; do not start the next stop", rn.MigrationsTimeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(rn.PollInterval):
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
