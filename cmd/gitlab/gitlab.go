package gitlab

import (
	"github.com/VojtechPastyrik/vpd/cmd/root"
	"github.com/spf13/cobra"
)

var Cmd = &cobra.Command{
	Use:   "gitlab",
	Short: "GitLab Utils",
}

func init() {
	root.RootCmd.AddCommand(Cmd)
}
