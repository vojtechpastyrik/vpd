package gitlab

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// State is one upgrade session of one host, kept on the workstation so a
// later run, status or report picks up where the last one stopped.
type State struct {
	Host    string       `json:"host"`
	Created time.Time    `json:"created"`
	Plan    Plan         `json:"plan"`
	Stops   []StopState  `json:"stops"`
	Finish  *FinishState `json:"finish,omitempty"`
}

type Plan struct {
	Facts    Facts                     `json:"facts"`
	Path     Path                      `json:"path"`
	Alerts   []Alert                   `json:"alerts,omitempty"`
	Notes    []UpgradeNote             `json:"notes,omitempty"`
	Migs     map[string]MigrationCount `json:"background_migrations,omitempty"`
	Problems []string                  `json:"problems,omitempty"`
}

const (
	StopPending = "pending"
	StopRunning = "running"
	StopDone    = "done"
	StopFailed  = "failed"
)

type StopState struct {
	PlanStop
	Status   string       `json:"status"`
	Phases   []PhaseState `json:"phases,omitempty"`
	Backup   string       `json:"backup,omitempty"`
	Warnings []string     `json:"warnings,omitempty"`
	Error    string       `json:"error,omitempty"`
}

type PhaseState struct {
	Name  string    `json:"name"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end,omitempty"`
}

type FinishState struct {
	Start          time.Time `json:"start"`
	End            time.Time `json:"end,omitempty"`
	RemovedBackups []string  `json:"removed_backups,omitempty"`
	Upgraded       []string  `json:"upgraded,omitempty"`
	RepoFixed      string    `json:"repo_fixed,omitempty"`
	RedisRestarted bool      `json:"redis_restarted"`
	RebootPending  bool      `json:"reboot_pending"`
	Rebooted       bool      `json:"rebooted"`
	KernelBefore   string    `json:"kernel_before,omitempty"`
	KernelAfter    string    `json:"kernel_after,omitempty"`
	Warnings       []string  `json:"warnings,omitempty"`
}

func DefaultStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".config", "vpd", "gitlab-upgrade")
}

func statePath(dir, host string) string {
	return filepath.Join(dir, host+".json")
}

func LoadState(dir, host string) (*State, error) {
	data, err := os.ReadFile(statePath(dir, host))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("no upgrade plan for %s; run `vpd gitlab upgrade plan --host %s` first", host, host)
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("read %s: %w", statePath(dir, host), err)
	}
	return &s, nil
}

// Save writes the state atomically; a crash mid-write must not lose the
// record of which stops are done.
func (s *State) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := statePath(dir, s.Host) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, statePath(dir, s.Host))
}

// Archive moves an existing state aside before a new plan replaces it.
func Archive(dir, host string) (string, error) {
	p := statePath(dir, host)
	if _, err := os.Stat(p); err != nil {
		return "", nil
	}
	dst := filepath.Join(dir, fmt.Sprintf("%s.%s.json", host, time.Now().Format("20060102-150405")))
	return dst, os.Rename(p, dst)
}

func (st *StopState) begin(name string) {
	st.Phases = append(st.Phases, PhaseState{Name: name, Start: time.Now()})
}

func (st *StopState) end() {
	if n := len(st.Phases); n > 0 && st.Phases[n-1].End.IsZero() {
		st.Phases[n-1].End = time.Now()
	}
}
