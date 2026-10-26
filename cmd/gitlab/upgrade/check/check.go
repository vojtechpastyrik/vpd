package check

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	parent_cmd "github.com/VojtechPastyrik/vpd/cmd/gitlab/upgrade"
	gitlab_utils "github.com/VojtechPastyrik/vpd/utils/gitlab"
	"github.com/spf13/cobra"
)

var Cmd = &cobra.Command{
	Use:   "check [host...]",
	Short: "One line per host: running version, newest in apt, stops needed",
	Example: `  vpd gitlab upgrade check --host vasops-gitlab
  vpd gitlab upgrade check vasops-gitlab pho-gitlab cnc-gitlab`,
	Run: func(cmd *cobra.Command, args []string) {
		hosts := args
		if len(hosts) == 0 {
			hosts = []string{parent_cmd.RequireHost()}
		}
		failed := false
		for _, alias := range hosts {
			h := parent_cmd.Host(alias)
			p, err := gitlab_utils.BuildPlan(context.Background(), h, parent_cmd.Upstream(h), gitlab_utils.PlanOptions{})
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", alias, err)
				failed = true
				continue
			}
			if parent_cmd.FlagOutput == "json" {
				b, _ := json.Marshal(map[string]any{"host": alias, "facts": p.Facts, "path": p.Path, "problems": p.Problems})
				fmt.Println(string(b))
				continue
			}
			fmt.Println(gitlab_utils.CheckLine(alias, p))
		}
		if failed {
			os.Exit(gitlab_utils.ExitAbortBefore)
		}
	},
}

func init() {
	parent_cmd.Cmd.AddCommand(Cmd)
}
