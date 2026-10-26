package report

import (
	"encoding/json"
	"fmt"
	"os"

	parent_cmd "github.com/VojtechPastyrik/vpd/cmd/gitlab/upgrade"
	"github.com/VojtechPastyrik/vpd/pkg/logger"
	gitlab_utils "github.com/VojtechPastyrik/vpd/utils/gitlab"
	"github.com/spf13/cobra"
)

var flagOut string

var Cmd = &cobra.Command{
	Use:     "report",
	Short:   "Markdown report of the upgrade session",
	Example: `  vpd gitlab upgrade report --host vasops-gitlab --out report.md`,
	Args:    cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		host := parent_cmd.RequireHost()
		st, err := gitlab_utils.LoadState(parent_cmd.FlagStateDir, host)
		if err != nil {
			logger.Fatal(err)
		}
		if parent_cmd.FlagOutput == "json" {
			b, _ := json.MarshalIndent(st, "", "  ")
			fmt.Println(string(b))
			return
		}
		md := st.ReportMarkdown()
		if flagOut != "" {
			if err := os.WriteFile(flagOut, []byte(md), 0o644); err != nil {
				logger.Fatalf("write %s: %v", flagOut, err)
			}
		}
		fmt.Print(md)
	},
}

func init() {
	parent_cmd.Cmd.AddCommand(Cmd)
	Cmd.Flags().StringVar(&flagOut, "out", "", "Also write the report to this file")
}
