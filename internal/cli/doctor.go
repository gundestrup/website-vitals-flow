package cli

import (
	"fmt"
	"os/exec"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/chrome"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/config"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/deep"
	"github.com/spf13/cobra"
)

func doctorCmd() *cobra.Command {
	var cfgPath string
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Report engine availability",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			fmt.Println("wvf doctor")
			if bin, ver, err := chrome.Find(cfg.BrowserPath); err == nil {
				fmt.Printf("  chrome      ✓ %s (%s)\n", ver, bin)
			} else {
				fmt.Printf("  chrome      ✗ %v\n", err)
			}
			if p, err := exec.LookPath("node"); err == nil {
				fmt.Printf("  node        ✓ %s\n", p)
			} else {
				fmt.Println("  node        ✗ not found — lighthouse audits unavailable")
			}
			if p, err := deep.FindBin(); err == nil {
				fmt.Printf("  lighthouse  ✓ %s\n", p)
			} else {
				fmt.Println("  lighthouse  ✗ not found — deep audits degrade to CDP-only")
			}
			fmt.Printf("  web-vitals  ✓ vendored (attribution)\n")
			if p, err := deep.FindAvifenc(); err == nil {
				fmt.Printf("  avifenc     ✓ %s (filmstrip avif)\n", p)
			} else {
				fmt.Println("  avifenc     ✗ not found — filmstrip stays webp")
			}
			fmt.Printf("  state_db    %s\n", cfg.StateDB)
			return nil
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", "path to wvf.yml")
	return cmd
}
