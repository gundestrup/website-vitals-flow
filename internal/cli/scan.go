package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/chrome"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/config"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/http3"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/httpcheck"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/measure"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/sitekey"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/store"
	"github.com/spf13/cobra"
)

func scanCmd() *cobra.Command {
	var (
		cfgPath string
		samples int
		timeout time.Duration
		settle  time.Duration
		asJSON  bool
	)
	cmd := &cobra.Command{
		Use:   "scan <url>",
		Short: "Measure a URL once and print the result",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			if samples > 0 {
				cfg.Samples = samples
			}
			if timeout > 0 {
				cfg.ScanTimeout = timeout
			}
			bin, ver, err := chrome.Find(cfg.BrowserPath)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), cfg.ScanTimeout)
			defer cancel()
			browser, stop, err := chrome.Launch(ctx, bin)
			if err != nil {
				return err
			}
			defer stop()
			started := time.Now()
			res, err := measure.Run(browser, args[0], cfg.Samples, settle)
			if err != nil {
				return err
			}
			if res.BrowserVersion == "" {
				res.BrowserVersion = ver
			}
			if hc, herr := httpcheck.Probe(ctx, res.URL); herr == nil {
				var assets []httpcheck.Asset
				if n := len(res.Samples); n > 0 {
					for _, r := range res.Samples[n-1].Responses {
						assets = append(assets, httpcheck.Asset{URL: r.URL, Status: r.Status, Headers: r.Headers})
					}
				}
				hc.Cache = httpcheck.CacheVerdicts(assets)
				res.HTTP = hc
				res.HTTP3 = http3.Probe(ctx, res.URL, hc.AltSvc)
			}
			if err := persist(ctx, cfg, res, started); err != nil {
				fmt.Fprintf(os.Stderr, "warn: store run: %v\n", err)
			}
			if asJSON {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(res)
			}
			printSummary(res)
			return nil
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", "path to wvf.yml")
	cmd.Flags().IntVar(&samples, "samples", 0, "loads per scan (default from config, 3)")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "whole-scan deadline (default 90s)")
	cmd.Flags().DurationVar(&settle, "settle", 1500*time.Millisecond, "post-load wait for late LCP/CLS")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit full result as JSON")
	return cmd
}

func persist(ctx context.Context, cfg config.Config, res *measure.Result, started time.Time) error {
	key, canon, err := sitekey.From(res.URL)
	if err != nil {
		return err
	}
	st, err := store.Open(ctx, cfg.StateDB)
	if err != nil {
		return err
	}
	defer st.Close()
	site, err := st.GetOrCreateSite(ctx, key, canon, cfg.KeepLast, cfg.MaxAgeDays)
	if err != nil {
		return err
	}
	last := res.Samples[len(res.Samples)-1]
	detail, _ := json.Marshal(res)
	var bytes int64
	var pages int64
	prot := ""
	for _, r := range last.Responses {
		bytes += r.EncodedBytes
		if r.MimeType == "text/html" {
			pages += r.EncodedBytes
		}
	}
	if len(last.Responses) > 0 {
		prot = last.Responses[0].Protocol
	}
	var ttfb, cls, tbt float64
	var fcp, lcp *float64
	n := float64(len(res.Samples))
	for _, s := range res.Samples {
		ttfb += s.TTFBms
		cls += s.CLS
		tbt += s.TBTms
	}
	run := &store.Run{
		SiteID:         site.ID,
		Engine:         "cdp",
		Trigger:        "cli",
		Status:         "ok",
		StartedAt:      started,
		FinishedAt:     time.Now(),
		BrowserVersion: res.BrowserVersion,
		TTFBms:         ttfb / n,
		FCPms:          fcp,
		LCPms:          lcp,
		CLS:            cls / n,
		TBTms:          tbt / n,
		RequestCount:   len(last.Responses),
		PageBytes:      bytes,
		HTTPProtocol:   prot,
		DetailJSON:     string(detail),
	}
	// median-ish headline values: use last sample's FCP/LCP
	if last.FCPms != nil {
		run.FCPms = last.FCPms
	}
	if last.LCPms != nil {
		run.LCPms = last.LCPms
	}
	_ = pages
	id, err := st.InsertRun(ctx, run)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "stored run %d under site %s\n", id, site.Key)
	return nil
}

func printSummary(res *measure.Result) {
	n := len(res.Samples)
	if n == 0 {
		fmt.Println("no samples")
		return
	}
	var ttfb, fcp, lcp, cls, tbt float64
	var fcpN, lcpN int
	for _, s := range res.Samples {
		ttfb += s.TTFBms
		cls += s.CLS
		tbt += s.TBTms
		if s.FCPms != nil {
			fcp += *s.FCPms
			fcpN++
		}
		if s.LCPms != nil {
			lcp += *s.LCPms
			lcpN++
		}
	}
	fmt.Printf("%s  (%d samples, %s)\n", res.URL, n, res.BrowserVersion)
	fmt.Printf("  TTFB %6.0f ms | FCP %6.0f ms | LCP %6.0f ms | CLS %.3f | TBT %5.0f ms\n",
		ttfb/float64(n), fcp/float64(max(fcpN, 1)), lcp/float64(max(lcpN, 1)),
		cls/float64(n), tbt/float64(n))
	last := res.Samples[n-1]
	fmt.Printf("  requests %d | DOM nodes %d | JS heap %.1f MB\n",
		len(last.Responses), last.DOMNodes, float64(last.JSHeap)/1048576)
}
