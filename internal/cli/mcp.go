// Package cli implements the wvf command surface (cobra).
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/gundestrup/website-vitals-flow/internal/mcpserver"
)

func mcpCmd() *cobra.Command {
	var cfgPath string
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Speak MCP over stdio — expose scan/history as agent tools",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, st, err := cfgAndStore(cfgPath)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			fmt.Fprintf(os.Stderr, "wvf mcp ready (tools: scan, sites, history, run, prune)\n")
			return mcpserver.Serve(ctx0, st, cfg, os.Stdin, os.Stdout)
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", cfgFlagHelp)
	return cmd
}
