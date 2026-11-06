// Package cli implements the wvf command surface (cobra).
package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/config"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/sitekey"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/store"
	"github.com/spf13/cobra"
)

func openStore(cfgPath string) (*store.Store, config.Config, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, cfg, err
	}
	st, err := store.Open(ctx0, cfg.StateDB)
	if err != nil {
		return nil, cfg, err
	}
	return st, cfg, nil
}

func sitesCmd() *cobra.Command {
	var cfgPath string
	cmd := &cobra.Command{Use: "sites", Short: "Manage monitored sites"}
	add := &cobra.Command{
		Use:   "add <url>",
		Short: "Register a site (or update its settings)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, cfg, err := openStore(cfgPath)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			key, canon, err := sitekey.From(args[0])
			if err != nil {
				return err
			}
			label, _ := cmd.Flags().GetString("label")
			schedule, _ := cmd.Flags().GetString("schedule")
			keep, _ := cmd.Flags().GetInt("keep-last")
			maxAge, _ := cmd.Flags().GetInt("max-age-days")
			deep, _ := cmd.Flags().GetBool("deep")
			only, _ := cmd.Flags().GetString("only")
			if keep <= 0 {
				keep = cfg.KeepLast
			}
			if maxAge <= 0 {
				maxAge = cfg.MaxAgeDays
			}
			site, err := st.UpsertSite(ctx0, store.SiteOpts{
				Key: key, URL: canon, Label: label, Schedule: schedule,
				KeepLast: keep, MaxAge: maxAge, Deep: deep, Only: only,
			})
			if err != nil {
				return err
			}
			fmt.Printf("site %s (%s) registered", site.Key, site.URL)
			if site.Schedule != "" {
				fmt.Printf(" schedule=%s", site.Schedule)
			}
			fmt.Printf(" keep_last=%d max_age=%dd\n", site.KeepLast, site.MaxAgeDays)
			return nil
		},
	}
	add.Flags().String("label", "", "display label")
	add.Flags().String("schedule", "", "scan interval like 30m/6h/daily (empty = manual only)")
	add.Flags().Int("keep-last", 0, "runs kept per site (default 100)")
	add.Flags().Int("max-age-days", 0, "days before runs expire (default 56)")
	add.Flags().Bool("deep", false, "deep scans (attribution+lighthouse) for this site")
	add.Flags().String("only", "", "comma list of checks to run (empty = all)")

	list := &cobra.Command{
		Use: "list", Short: "List sites",
		RunE: func(_ *cobra.Command, _ []string) error {
			st, _, err := openStore(cfgPath)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			sites, err := st.ListSites(ctx0)
			if err != nil {
				return err
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			_, _ = fmt.Fprintln(w, "KEY\tURL\tSCHEDULE\tKEEP\tAGE\tDEEP\tONLY")
			for _, s := range sites {
				_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%dd\t%v\t%s\n",
					s.Key, s.URL, s.Schedule, s.KeepLast, s.MaxAgeDays, s.Deep, s.Only)
			}
			return w.Flush()
		},
	}
	rm := &cobra.Command{
		Use: "remove <key>", Short: "Delete a site and its runs", Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			st, _, err := openStore(cfgPath)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			key, _, err := sitekey.From(args[0])
			if err != nil {
				return err
			}
			if err := st.RemoveSite(ctx0, key); err != nil {
				return err
			}
			fmt.Printf("site %s removed\n", key)
			return nil
		},
	}
	cmd.PersistentFlags().StringVar(&cfgPath, "config", "", cfgFlagHelp)
	cmd.AddCommand(add, list, rm)
	return cmd
}

func pruneCmd() *cobra.Command {
	var cfgPath, only string
	cmd := &cobra.Command{
		Use:   "prune",
		Short: "Apply retention rules now (all sites or --site)",
		RunE: func(_ *cobra.Command, _ []string) error {
			st, _, err := openStore(cfgPath)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			sites, err := st.ListSites(ctx0)
			if err != nil {
				return err
			}
			var total int64
			for _, s := range sites {
				if only != "" && s.Key != only {
					continue
				}
				n, err := st.PruneSiteNow(ctx0, s.ID)
				if err != nil {
					return err
				}
				if n > 0 {
					fmt.Printf("%s: %d runs pruned\n", s.Key, n)
				}
				total += n
			}
			fmt.Printf("prune done (%d runs dropped)\n", total)
			return nil
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", cfgFlagHelp)
	cmd.Flags().StringVar(&only, "site", "", "only this site key")
	return cmd
}

func reportCmd() *cobra.Command {
	var cfgPath string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "report <id>",
		Short: "Show a stored run's detail",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			var id int64
			if _, err := fmt.Sscanf(args[0], "%d", &id); err != nil {
				return fmt.Errorf("bad run id %q", args[0])
			}
			st, _, err := openStore(cfgPath)
			if err != nil {
				return err
			}
			defer func() { _ = st.Close() }()
			r, err := st.RunByID(ctx0, id)
			if err != nil {
				return err
			}
			if asJSON {
				out, _ := store.PrettyDetail(r)
				fmt.Println(out)
				return nil
			}
			fmt.Printf("run %d site=%d engine=%s trigger=%s status=%s\n",
				r.ID, r.SiteID, r.Engine, r.Trigger, r.Status)
			fmt.Printf("  %s → %s  browser=%s\n", r.StartedAt.Format("15:04:05"), r.FinishedAt.Format("15:04:05"), r.BrowserVersion)
			fmt.Printf("  TTFB %.0fms | FCP %s | LCP %s | CLS %.3f | TBT %.0fms\n",
				r.TTFBms, f2s(r.FCPms), f2s(r.LCPms), r.CLS, r.TBTms)
			fmt.Printf("  %d requests, %d bytes, %s\n", r.RequestCount, r.PageBytes, r.HTTPProtocol)
			if r.Error != "" {
				fmt.Printf("  error: %s\n", r.Error)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", cfgFlagHelp)
	cmd.Flags().BoolVar(&asJSON, "json", false, "print detail_json")
	return cmd
}

func f2s(f *float64) string {
	if f == nil {
		return "  —  "
	}
	return fmt.Sprintf("%.0fms", *f)
}
