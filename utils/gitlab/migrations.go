package gitlab

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// BatchedMigration is one batched background migration that is not finished.
type BatchedMigration struct {
	Class       string `json:"class"`
	Table       string `json:"table"`
	Status      int    `json:"status"`
	Jobs        int    `json:"jobs"`
	MaxAttempts int    `json:"max_attempts"`
	OnHoldUntil string `json:"on_hold_until,omitempty"`
}

func (m BatchedMigration) String() string {
	s := fmt.Sprintf("%s on %s (jobs %d", m.Class, m.Table, m.Jobs)
	if m.MaxAttempts > 1 {
		s += fmt.Sprintf(", attempts up to %d", m.MaxAttempts)
	}
	if m.OnHoldUntil != "" {
		s += ", on hold until " + m.OnHoldUntil
	}
	return s + ")"
}

// Status 3 is finished, 6 finalized.
const batchedScript = `
gitlab-psql -d gitlabhq_production -tA -F'|' <<'SQL'
SELECT m.job_class_name, m.table_name, m.status,
  (SELECT count(*) FROM batched_background_migration_jobs j WHERE j.batched_background_migration_id = m.id),
  COALESCE((SELECT max(attempts) FROM batched_background_migration_jobs j WHERE j.batched_background_migration_id = m.id), 0),
  COALESCE(to_char(m.on_hold_until, 'YYYY-MM-DD HH24:MI'), '')
FROM batched_background_migrations m
WHERE m.status NOT IN (3, 6)
ORDER BY m.id;
SQL
`

func PendingBatched(ctx context.Context, h Host) ([]BatchedMigration, error) {
	out, err := h.Run(ctx, batchedScript)
	if err != nil {
		return nil, fmt.Errorf("query batched background migrations: %w", err)
	}
	var list []BatchedMigration
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(l, "|")
		if len(f) < 6 {
			continue
		}
		list = append(list, BatchedMigration{
			Class: f[0], Table: f[1], Status: atoi(f[2]),
			Jobs: atoi(f[3]), MaxAttempts: atoi(f[4]), OnHoldUntil: f[5],
		})
	}
	return list, nil
}

// LegacyNotFinished counts legacy background migrations that are not done.
// The rake task takes about 20 seconds.
func LegacyNotFinished(ctx context.Context, h Host, v Version) (int, error) {
	// gitlab:background_migrations:list exists from 18.9; older versions have :status.
	task := "gitlab:background_migrations:list"
	if v.Major < 18 || (v.Major == 18 && v.Minor <= 8) {
		task = "gitlab:background_migrations:status"
	}
	out, err := h.Run(ctx, "gitlab-rake "+task+" 2>/dev/null")
	if err != nil {
		return 0, fmt.Errorf("gitlab-rake %s: %w", task, err)
	}
	return CountLegacyNotFinished(out), nil
}

var notFinishedRe = regexp.MustCompile(`(?i)\b(pending|active|paused|failed|finalizing)\b`)

// CountLegacyNotFinished reads the status column of the rake task's pipe
// table, so a class or table name containing "failed" does not count.
// Output without that header (the older :status format) falls back to
// matching whole lines.
func CountLegacyNotFinished(out string) int {
	col := -1
	n := 0
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(l, "|")
		if col < 0 {
			for i, h := range f {
				if strings.TrimSpace(h) == "status" {
					col = i
				}
			}
			continue
		}
		if len(f) > col {
			s := strings.ToLower(strings.TrimSpace(f[col]))
			if notFinishedRe.MatchString(s) && notFinishedRe.FindString(s) == s {
				n++
			}
		}
	}
	if col >= 0 {
		return n
	}
	for _, l := range strings.Split(out, "\n") {
		if notFinishedRe.MatchString(l) {
			n++
		}
	}
	return n
}
