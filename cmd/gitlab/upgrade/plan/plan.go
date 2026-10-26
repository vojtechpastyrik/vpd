package plan

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	parent_cmd "github.com/VojtechPastyrik/vpd/cmd/gitlab/upgrade"
	"github.com/VojtechPastyrik/vpd/pkg/logger"
	gitlab_utils "github.com/VojtechPastyrik/vpd/utils/gitlab"
	"github.com/spf13/cobra"
)

var (
	flagTarget       string
	flagSkipOptional bool
	flagOut          string
)

var Cmd = &cobra.Command{
	Use:   "plan",
	Short: "Read the host and GitLab's upgrade data, save the plan",
	Long: `Reads the host (version, edition, apt versions, migrations, disk, features),
GitLab's upgrade_path.yml, alerts, background migration counts and the upgrade
notes of every version on the way, and saves the plan as the host's state.

Without --target the plan goes to the newest version the host's apt repository
offers. An existing state is archived next to it.`,
	Example: `  vpd gitlab upgrade plan --host vasops-gitlab
  vpd gitlab upgrade plan --host vasops-gitlab --target 19.5 --skip-optional-patch --out plan.md`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		host := parent_cmd.RequireHost()
		h := parent_cmd.Host(host)
		p, err := gitlab_utils.BuildPlan(context.Background(), h, parent_cmd.Upstream(h), gitlab_utils.PlanOptions{
			Target:       flagTarget,
			SkipOptional: flagSkipOptional,
		})
		if err != nil {
			logger.Fatalf("plan for %s: %v", host, err)
		}
		if archived, err := gitlab_utils.Archive(parent_cmd.FlagStateDir, host); err != nil {
			logger.Fatalf("archive old state: %v", err)
		} else if archived != "" {
			fmt.Fprintf(os.Stderr, "previous state archived as %s\n", archived)
		}
		st := &gitlab_utils.State{Host: host, Created: time.Now(), Plan: p}
		for _, s := range p.Path.Stops {
			st.Stops = append(st.Stops, gitlab_utils.StopState{PlanStop: s, Status: gitlab_utils.StopPending})
		}
		if err := st.Save(parent_cmd.FlagStateDir); err != nil {
			logger.Fatalf("save state: %v", err)
		}

		md := p.Markdown(host)
		if flagOut != "" {
			if err := os.WriteFile(flagOut, []byte(md), 0o644); err != nil {
				logger.Fatalf("write %s: %v", flagOut, err)
			}
		}
		if parent_cmd.FlagOutput == "json" {
			b, _ := json.MarshalIndent(p, "", "  ")
			fmt.Println(string(b))
		} else {
			fmt.Print(md)
		}
		if len(p.Problems) > 0 {
			os.Exit(gitlab_utils.ExitWarnings)
		}
	},
}

func init() {
	parent_cmd.Cmd.AddCommand(Cmd)
	Cmd.Flags().StringVarP(&flagTarget, "target", "t", "", "Target version X.Y or X.Y.Z (default: newest in the host's apt repository)")
	Cmd.Flags().BoolVar(&flagSkipOptional, "skip-optional-patch", false, "Leave out the latest patch of the running minor")
	Cmd.Flags().StringVar(&flagOut, "out", "", "Also write the Markdown plan to this file")
}
