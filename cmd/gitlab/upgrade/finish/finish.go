package finish

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

var opts gitlab_utils.FinishOptions

var Cmd = &cobra.Command{
	Use:   "finish",
	Short: "Clean up backups, upgrade OS packages, restart stale Redis, optionally reboot",
	Long: `After the last stop: deletes the database-only backups the package wrote
before each upgrade, upgrades the OS packages with GitLab on hold, restarts
Redis when it still runs the binary from before the upgrade, and reboots when
--reboot is set and the OS asks for it. Ends with the smoke test.`,
	Example: `  vpd gitlab upgrade finish --host vasops-gitlab
  vpd gitlab upgrade finish --host vasops-gitlab --fix-repo --reboot`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		host := parent_cmd.RequireHost()
		r := parent_cmd.Reporter()
		st, err := gitlab_utils.LoadState(parent_cmd.FlagStateDir, host)
		if err != nil {
			st = &gitlab_utils.State{Host: host, Created: time.Now()}
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		fn := &gitlab_utils.Finisher{
			Host:     parent_cmd.Host(host),
			R:        r,
			State:    st,
			StateDir: parent_cmd.FlagStateDir,
			Opts:     opts,
		}
		parent_cmd.Exit(r, fn.Run(ctx))
	},
}

func init() {
	parent_cmd.Cmd.AddCommand(Cmd)
	Cmd.Flags().BoolVar(&opts.Reboot, "reboot", false, "Reboot when the OS asks for it, then wait for GitLab")
	Cmd.Flags().BoolVar(&opts.KeepDBBackups, "keep-db-backups", false, "Keep the database-only backups the package wrote")
	Cmd.Flags().BoolVar(&opts.FixRepo, "fix-repo", false, "Switch the GitLab apt repository suite to the OS codename")
	Cmd.Flags().BoolVar(&opts.SkipRedis, "skip-redis", false, "Do not restart a Redis older than the package install")
}
