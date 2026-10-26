package status

import (
	"context"
	"encoding/json"
	"fmt"

	parent_cmd "github.com/VojtechPastyrik/vpd/cmd/gitlab/upgrade"
	"github.com/VojtechPastyrik/vpd/pkg/logger"
	gitlab_utils "github.com/VojtechPastyrik/vpd/utils/gitlab"
	"github.com/spf13/cobra"
)

var flagLegacy bool

var Cmd = &cobra.Command{
	Use:   "status",
	Short: "Live view: phase, pending migrations, services, tmux sessions",
	Example: `  vpd gitlab upgrade status --host vasops-gitlab
  vpd gitlab upgrade status --host vasops-gitlab --legacy`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		host := parent_cmd.RequireHost()
		st, err := gitlab_utils.LoadState(parent_cmd.FlagStateDir, host)
		if err != nil {
			st = &gitlab_utils.State{Host: host}
		}
		s, err := gitlab_utils.ReadStatus(context.Background(), parent_cmd.Host(host), st, flagLegacy)
		if err != nil {
			logger.Fatalf("status of %s: %v", host, err)
		}
		if parent_cmd.FlagOutput == "json" {
			b, _ := json.Marshal(s)
			fmt.Println(string(b))
			return
		}
		fmt.Print(s.Text())
	},
}

func init() {
	parent_cmd.Cmd.AddCommand(Cmd)
	Cmd.Flags().BoolVar(&flagLegacy, "legacy", false, "Also count legacy background migrations (rake, about 20 s)")
}
