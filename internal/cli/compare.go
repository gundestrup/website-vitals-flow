package cli

import (
	"fmt"

	"github.com/gundestrup/website-vitals-flow/internal/sitekey"
	"github.com/gundestrup/website-vitals-flow/internal/store"
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
		RunE: func(_ *cobra.Command, args []string) error {
			return runCompare(cfgPath, baseID, thresh, args[0])
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", cfgFlagHelp)
	cmd.Flags().Int64Var(&baseID, "base", 0, "baseline run id (default: oldest)")
	cmd.Flags().Float64Var(&thresh, "thresh", 15, "regression flag threshold %")
	return cmd
}

// runCompare diffs the latest run for siteArg against a baseline run —
// baseID when given, else the oldest stored run.
func runCompare(cfgPath string, baseID int64, thresh float64, siteArg string) error {
	st, _, err := openStore(cfgPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()
	key, _, err := sitekey.From(siteArg)
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
}

func jobsCmd() *cobra.Command {
	var cfgPath string
	var limit int
	cmd := &cobra.Command{
		Use:   "jobs",
		Short: "List recent jobs (queued/running/done/failed)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			st, _, err := openStore(cfgPath)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			jobs, err := st.RecentJobs(ctx0, limit)
			if err != nil {
				return err
			}
			for _, j := range jobs {
				fmt.Printf("%-4d %-7s %-9s %s %s\n",
					j.ID, j.State, j.Kind, j.QueuedAt[5:19], j.Detail)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", cfgFlagHelp)
	cmd.Flags().IntVar(&limit, "limit", 20, "max jobs to list")
	return cmd
}
