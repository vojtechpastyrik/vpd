package run

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	parent_cmd "github.com/VojtechPastyrik/vpd/cmd/gitlab/upgrade"
	gitlab_utils "github.com/VojtechPastyrik/vpd/utils/gitlab"
	"github.com/spf13/cobra"
)

var (
	flagGate              bool
	flagMigrationsTimeout time.Duration
)

var Cmd = &cobra.Command{
	Use:   "run",
	Short: "Run the planned stops: backup, pinned install, migration wait, smoke test",
	Long: `Runs every stop of the saved plan that is not done yet. A stop whose package is
already installed resumes at the migration wait, so running again after an
interruption or a gate continues where it stopped.

With --gate the command exits with code 4 after each stop but the last, for the
manual checks a script cannot do (git push, a CI job, a registry pull). Use it
on production.`,
	Example: `  vpd gitlab upgrade run --host vasops-gitlab --gate
  vpd gitlab upgrade run --host vasops-gitlab -o json > upgrade.log`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		host := parent_cmd.RequireHost()
		r := parent_cmd.Reporter()
		st, err := gitlab_utils.LoadState(parent_cmd.FlagStateDir, host)
		if err != nil {
			parent_cmd.Exit(r, err)
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		rn := &gitlab_utils.Runner{
			Host:               parent_cmd.Host(host),
			R:                  r,
			State:              st,
			StateDir:           parent_cmd.FlagStateDir,
			Gate:               flagGate,
			MigrationsTimeout:  flagMigrationsTimeout,
			PollInterval:       20 * time.Second,
			Heartbeat:          5 * time.Minute,
			LegacyCheckEvery:   6,
			HealthAttempts:     18,
			HealthRetryBackoff: 10 * time.Second,
		}
		parent_cmd.Exit(r, rn.Run(ctx))
	},
}

func init() {
	parent_cmd.Cmd.AddCommand(Cmd)
	Cmd.Flags().BoolVar(&flagGate, "gate", false, "Exit with code 4 after each stop but the last, for manual checks")
	Cmd.Flags().DurationVar(&flagMigrationsTimeout, "migrations-timeout", 2*time.Hour, "Give up waiting for background migrations after this long")
}
