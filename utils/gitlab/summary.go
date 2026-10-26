package gitlab

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ReportMarkdown summarizes a finished session from its state.
func (s *State) ReportMarkdown() string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	f := s.Plan.Facts
	last := f.Package
	for _, st := range s.Stops {
		if st.Status == StopDone {
			last = st.Package
		}
	}
	w("# GitLab upgrade report: %s\n", s.Host)
	w("- from `%s` to `%s` (%s)", f.Package, last, strings.ToUpper(f.Edition))
	if start, end := s.window(); !start.IsZero() {
		w("- window: %s to %s (%s)", start.Format("2006-01-02 15:04"), end.Format("15:04"), end.Sub(start).Round(time.Minute))
	}
	w("")
	w("## Stops\n")
	w("| Stop | Kind | Status | Backup | Phases |")
	w("|---|---|---|---|---|")
	for _, st := range s.Stops {
		var ph []string
		for _, p := range st.Phases {
			if !p.End.IsZero() {
				ph = append(ph, fmt.Sprintf("%s %s", p.Name, p.End.Sub(p.Start).Round(time.Second)))
			}
		}
		w("| `%s` | %s | %s | %s | %s |", st.Package, st.Kind, st.Status, st.Backup, strings.Join(ph, ", "))
	}
	w("")
	if s.Finish != nil {
		fs := s.Finish
		w("## Finish\n")
		w("- removed database-only backups: %d", len(fs.RemovedBackups))
		w("- OS packages upgraded: %d", len(fs.Upgraded))
		for _, u := range fs.Upgraded {
			w("  - %s", u)
		}
		if fs.RepoFixed != "" {
			w("- apt repository suite switched: %s", fs.RepoFixed)
		}
		w("- redis restarted: %t", fs.RedisRestarted)
		w("- rebooted: %t, kernel %s -> %s", fs.Rebooted, fs.KernelBefore, fs.KernelAfter)
		if fs.RebootPending {
			w("- **reboot still pending**")
		}
		w("")
	}
	var warns []string
	for _, st := range s.Stops {
		warns = append(warns, st.Warnings...)
		if st.Error != "" {
			warns = append(warns, fmt.Sprintf("stop %s: %s", st.Package, st.Error))
		}
	}
	if s.Finish != nil {
		warns = append(warns, s.Finish.Warnings...)
	}
	if len(warns) > 0 {
		w("## Warnings\n")
		for _, x := range warns {
			w("- %s", x)
		}
	}
	return b.String()
}

func (s *State) window() (start, end time.Time) {
	for _, st := range s.Stops {
		for _, p := range st.Phases {
			if start.IsZero() || p.Start.Before(start) {
				start = p.Start
			}
			if p.End.After(end) {
				end = p.End
			}
		}
	}
	if s.Finish != nil && s.Finish.End.After(end) {
		end = s.Finish.End
	}
	return
}

// Status is the live view `vpd gitlab upgrade status` prints.
type Status struct {
	Host        string             `json:"host"`
	Version     string             `json:"version"`
	Stop        string             `json:"stop,omitempty"`
	Phase       string             `json:"phase,omitempty"`
	PhaseSince  string             `json:"phase_since,omitempty"`
	Batched     []BatchedMigration `json:"batched"`
	Legacy      int                `json:"legacy"`
	NotRunning  []string           `json:"not_running"`
	Sessions    []string           `json:"tmux_sessions,omitempty"`
	LastLogLine string             `json:"last_log_line,omitempty"`
}

func ReadStatus(ctx context.Context, h Host, s *State, withLegacy bool) (Status, error) {
	f, err := ReadFacts(ctx, h, false)
	if err != nil {
		return Status{}, err
	}
	out := Status{Host: s.Host, Version: f.Version, Legacy: -1}
	var active *StopState
	for i := range s.Stops {
		if s.Stops[i].Status == StopRunning || s.Stops[i].Status == StopFailed {
			active = &s.Stops[i]
		}
	}
	if active != nil {
		out.Stop = active.Package
		if n := len(active.Phases); n > 0 {
			p := active.Phases[n-1]
			out.Phase = p.Name
			out.PhaseSince = p.Start.Format("15:04:05")
			if active.Status == StopFailed {
				out.Phase += " (failed: " + active.Error + ")"
			}
			if p.Name == "backup" || p.Name == "install" {
				v, _ := ParseVersion(active.Version)
				line, _ := h.Run(ctx, fmt.Sprintf("tail -n1 %s/%s-%s.log 2>/dev/null", remoteLogDir, v, p.Name))
				out.LastLogLine = truncate(strings.TrimSpace(line), 160)
			}
		}
	}
	if out.Batched, err = PendingBatched(ctx, h); err != nil {
		return out, err
	}
	if withLegacy {
		v, _ := ParseVersion(f.Version)
		if out.Legacy, err = LegacyNotFinished(ctx, h, v); err != nil {
			return out, err
		}
	}
	st, _ := h.Run(ctx, "gitlab-ctl status")
	for _, l := range strings.Split(strings.TrimSpace(st), "\n") {
		if l != "" && !strings.HasPrefix(l, "run:") {
			out.NotRunning = append(out.NotRunning, l)
		}
	}
	tm, _ := h.Run(ctx, "tmux ls -F '#{session_name}' 2>/dev/null | grep '^gu-' || true")
	out.Sessions = strings.Fields(tm)
	return out, nil
}

func (st Status) Text() string {
	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }
	w("%s: GitLab %s", st.Host, st.Version)
	if st.Stop != "" {
		w("stop %s, phase %s since %s", st.Stop, st.Phase, st.PhaseSince)
	}
	if st.LastLogLine != "" {
		w("last log line: %s", st.LastLogLine)
	}
	w("batched migrations pending: %d", len(st.Batched))
	for _, m := range st.Batched {
		w("  %s", m)
	}
	if st.Legacy >= 0 {
		w("legacy migrations not finished: %d", st.Legacy)
	}
	if len(st.NotRunning) > 0 {
		w("services not running: %s", strings.Join(st.NotRunning, "; "))
	} else {
		w("all services running")
	}
	if len(st.Sessions) > 0 {
		w("tmux sessions on host: %s", strings.Join(st.Sessions, ", "))
	}
	return b.String()
}
