package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/mcpserver"
)

func mcpCmd() *cobra.Command {
	var cfgPath string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Speak MCP over stdio — expose scan/history as agent tools",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, st, err := cfgAndStore(cfgPath)
			if err != nil {
				return err
			}
			defer st.Close()
			fmt.Fprintf(os.Stderr, "wvf mcp ready (tools: scan, sites, history, run, prune)\n")
			return mcpserver.Serve(ctx0, st, cfg, os.Stdin, os.Stdout)
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", "path to wvf.yml")
	return cmd
}
