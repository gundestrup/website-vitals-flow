package cli

import (
	"fmt"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/store"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/sitekey"
	"github.com/spf13/cobra"
)

func compareCmd() *cobra.Command {
	var cfgPath string
	var baseID int64
	var thresh float64
	cmd := &cobra.Command{
		Use:   "compare <site> [--base <run-id>]",
		Short: "Diff latest run vs a baseline run",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, _, err := openStore(cfgPath)
			if err != nil {
				return err
			}
			defer st.Close()
			key, _, err := sitekey.From(args[0])
			if err != nil {
				return err
			}
			site, err := st.SiteByKey(ctx0, key)
			if err != nil {
				return fmt.Errorf("unknown site %q", key)
			}
			runs, err := st.RunsForSite(ctx0, site.ID, 200)
			if err != nil || len(runs) < 2 {
				return fmt.Errorf("site %s needs ≥2 runs to compare", key)
			}
			latest := runs[0]
			base := runs[len(runs)-1] // oldest = implicit baseline
			if baseID > 0 {
				for i := range runs {
					if runs[i].ID == baseID {
						base = runs[i]
					}
				}
			}
			fmt.Printf("compare %s: run %d → run %d\n", key, base.ID, latest.ID)
			for _, d := range store.CompareRuns(&base, &latest) {
				mark := ""
				if d.Worsened(thresh) {
					mark = "  ← regressed"
				}
				fmt.Println(" " + d.String() + mark)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", "path to wvf.yml")
	cmd.Flags().Int64Var(&baseID, "base", 0, "baseline run id (default: oldest)")
	cmd.Flags().Float64Var(&thresh, "thresh", 15, "regression flag threshold %")
	return cmd
}
