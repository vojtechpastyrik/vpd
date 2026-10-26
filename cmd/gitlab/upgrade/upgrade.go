package upgrade

import (
	"errors"
	"os"

	parent_cmd "github.com/VojtechPastyrik/vpd/cmd/gitlab"
	"github.com/VojtechPastyrik/vpd/pkg/logger"
	"github.com/VojtechPastyrik/vpd/pkg/sshexec"
	gitlab_utils "github.com/VojtechPastyrik/vpd/utils/gitlab"
	"github.com/spf13/cobra"
)

var (
	FlagHost     string
	FlagOutput   string
	FlagStateDir string
)

var Cmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Upgrade a Linux package (Omnibus) GitLab through the required stops",
	Long: `Upgrade a single-node Linux package GitLab, CE or EE, one required stop at a time.

  check    one line: running version, newest in apt, stops needed
  plan     read the host and GitLab's upgrade data, save the plan
  run      run the planned stops: backup, pinned apt install, migration wait, smoke test
  status   live view of the running upgrade
  finish   clean up backups, upgrade OS packages, restart stale Redis, reboot
  report   Markdown report of the session

Exit codes: 0 ok, 1 stopped before the package switched, 2 stopped after the
package switched (needs attention), 3 done with warnings, 4 waiting at a gate.
State lives in ~/.config/vpd/gitlab-upgrade/<host>.json.`,
}

func init() {
	parent_cmd.Cmd.AddCommand(Cmd)
	Cmd.PersistentFlags().StringVarP(&FlagHost, "host", "H", "", "SSH alias of the GitLab host")
	Cmd.PersistentFlags().StringVarP(&FlagOutput, "output", "o", "text", "Output format: text or json")
	Cmd.PersistentFlags().StringVar(&FlagStateDir, "state-dir", gitlab_utils.DefaultStateDir(), "Directory for upgrade state files")
}

func RequireHost() string {
	if FlagHost == "" {
		logger.Fatal("--host is required")
	}
	return FlagHost
}

func Host(alias string) *sshexec.Client {
	return sshexec.New(alias)
}

func Reporter() *gitlab_utils.Reporter {
	return &gitlab_utils.Reporter{W: os.Stdout, JSON: FlagOutput == "json"}
}

// Upstream reads GitLab's public data from here, and through the host when
// this workstation cannot reach gitlab.com.
func Upstream(h gitlab_utils.Host) gitlab_utils.Upstream {
	return gitlab_utils.Upstream{Fetcher: gitlab_utils.FallbackFetcher{
		gitlab_utils.HTTPFetcher{},
		gitlab_utils.HostFetcher{Host: h},
	}}
}

// Exit ends the command with the exit code an Abort carries.
func Exit(r *gitlab_utils.Reporter, err error) {
	if err == nil {
		os.Exit(gitlab_utils.ExitOK)
	}
	var a *gitlab_utils.Abort
	if errors.As(err, &a) {
		if a.Code == gitlab_utils.ExitAbortBefore || a.Code == gitlab_utils.ExitAbortAfter {
			r.Error(a.Msg)
		}
		os.Exit(a.Code)
	}
	r.Error(err.Error())
	os.Exit(gitlab_utils.ExitAbortBefore)
}
