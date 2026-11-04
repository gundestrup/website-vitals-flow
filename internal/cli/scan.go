package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/chrome"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/config"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/deep"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/findings"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/jobs"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/psi"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/http3"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/httpcheck"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/measure"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/sitekey"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/store"
	"github.com/spf13/cobra"
)

func scanCmd() *cobra.Command {
	var (
		cfgPath  string
		samples  int
		timeout  time.Duration
		settle   time.Duration
		asJSON   bool
		htmlOut  string
		deepMode bool
		psiMode  bool
		onlyCSV  string
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
			only := findings.ParseOnly(onlyCSV)
			res, err := measure.Run(browser, args[0], cfg.Samples, settle)
			if err != nil {
				return err
			}
			if res.BrowserVersion == "" {
				res.BrowserVersion = ver
			}
			if findings.Want(only, "http") {
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
			}
			if deepMode && findings.Want(only, "deep") {
				if a, aerr := deep.CollectAttribution(browser, res.URL); aerr == nil {
					res.Attribution = a
				} else {
					fmt.Fprintf(os.Stderr, "warn: attribution: %v\n", aerr)
				}
				if bin, berr := deep.FindBin(); berr == nil {
					if lh, lerr := deep.Lighthouse(ctx, bin, res.URL); lerr == nil {
						res.Lighthouse = lh
					} else {
						fmt.Fprintf(os.Stderr, "warn: lighthouse: %v\n", lerr)
					}
				} else {
					fmt.Fprintln(os.Stderr, "warn: lighthouse not on PATH — skipping audits")
				}
			}
			res.Findings = findings.Filter(findings.Analyze(res), only)
			if psiMode {
				if r, perr := psi.Fetch(ctx, res.URL, cfg.PSIKey); perr == nil {
					res.PSI = r
				} else {
					fmt.Fprintf(os.Stderr, "warn: psi: %v\n", perr)
				}
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
			if htmlOut != "" {
				if err := writeHTMLReport(htmlOut, res); err != nil {
					return err
				}
				fmt.Fprintf(os.Stderr, "report: %s\n", htmlOut)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", "path to wvf.yml")
	cmd.Flags().IntVar(&samples, "samples", 0, "loads per scan (default from config, 3)")
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "whole-scan deadline (default 90s)")
	cmd.Flags().DurationVar(&settle, "settle", 1500*time.Millisecond, "post-load wait for late LCP/CLS")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit full result as JSON")
	cmd.Flags().StringVar(&htmlOut, "html", "", "write self-contained HTML report to file")
	cmd.Flags().BoolVar(&deepMode, "deep", false, "add web-vitals attribution + lighthouse audits when available")
	cmd.Flags().BoolVar(&psiMode, "psi", false, "also fetch PageSpeed Insights (public FQDNs only)")
	cmd.Flags().StringVar(&onlyCSV, "only", "", "comma list to focus: lcp,cls,tbt,ttfb,fcp,http,deep — skips other checks")
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
	id, err := jobs.Persist(ctx, st, site, res, started)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "stored run %d under site %s\n", id, site.Key)
	return nil
}

func writeHTMLReport(path string, res *measure.Result) error {
	data, err := json.Marshal(res)
	if err != nil {
		return err
	}
	safe := strings.ReplaceAll(string(data), "</", "<\\/")
	html := strings.ReplaceAll(reportTmpl, "__REPORT_DATA__", safe)
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(html), 0o644)
}

const reportTmpl = `<!doctype html><html><head><meta charset=utf-8><title>wvf report</title>
<style>body{font-family:system-ui;max-width:900px;margin:2em auto;padding:0 1em}
table{border-collapse:collapse;width:100%}td,th{padding:.35em;border-bottom:1px solid #ddd;text-align:left}
pre{background:#f5f5f5;padding:1em;overflow:auto}</style></head><body>
<h1>wvf report</h1><div id=out></div>
<script>const DATA = __REPORT_DATA__;
const s=DATA.samples[DATA.samples.length-1];
out.innerHTML='<p>'+DATA.url+' — '+DATA.browser_version+'</p>'
+'<table><tr><th>metric</th><th>ms</th></tr>'
+'<tr><td>TTFB</td><td>'+Math.round(s.ttfb_ms)+'</td></tr>'
+'<tr><td>FCP</td><td>'+Math.round(s.fcp_ms||0)+'</td></tr>'
+'<tr><td>LCP</td><td>'+Math.round(s.lcp_ms||0)+' ('+(s.lcp_element||'')+')</td></tr>'
+'<tr><td>CLS</td><td>'+s.cls.toFixed(3)+'</td></tr>'
+'<tr><td>TBT</td><td>'+Math.round(s.tbt_ms)+'</td></tr></table>'
+'<h2>'+s.responses.length+' responses</h2><pre>'+
s.responses.map(r=>r.protocol+' '+r.status+' '+(r.content_encoding||'-')+' '+r.encoded_bytes+'B '+r.url.slice(0,90)).join('
')+'</pre>';
</script></body></html>`

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
	for _, f := range res.Findings {
		fmt.Printf("  [%s] %s\n", f.Severity, f.Title)
	}
}
