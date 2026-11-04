// Package cli wires the cobra command tree for wvf.
package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// ctx0 is the process-lifetime context for DB-facing commands.
var ctx0 = context.Background()

var version = "dev"

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "wvf",
		Short: "website-vitals-flow — contained localhost web vitals tester",
		Long:  "Scan localhost or FQDN targets for web vitals, HTTP behaviour and audits. One binary, one SQLite file, one dashboard.",
	}
	root.AddCommand(scanCmd(), versionCmd(), sitesCmd(), pruneCmd(), reportCmd(), serveCmd(), doctorCmd())
	return root
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run:   func(cmd *cobra.Command, args []string) { fmt.Println("wvf", version) },
	}
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
