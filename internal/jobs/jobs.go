// Package jobs is the single-slot scan runner shared by the CLI,
// scheduler, and web UI — one scan at a time, one code path, all runs
// land in the same store.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/chrome"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/config"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/deep"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/http3"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/httpcheck"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/measure"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/store"
)

// Runner is a single-slot scan executor fed by a buffered queue.
type Runner struct {
	st    *store.Store
	cfg   config.Config
	queue chan int64
}

// New builds a runner over the store.
func New(st *store.Store, cfg config.Config) *Runner {
	return &Runner{st: st, cfg: cfg, queue: make(chan int64, 32)}
}

// Enqueue registers a job and offers it to the worker. kind is
// "scan"; trigger lands on the run record (cli|manual|scheduled).
func (r *Runner) Enqueue(ctx context.Context, siteID int64, kind string, deep bool) (int64, error) {
	id, err := r.st.InsertJob(ctx, kind, siteID, deep)
	if err != nil {
		return 0, err
	}
	select {
	case r.queue <- id:
	default:
		return id, fmt.Errorf("queue full")
	}
	return id, nil
}

// Start runs the worker loop until ctx ends.
func (r *Runner) Start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-r.queue:
			r.exec(ctx, id)
		}
	}
}

func (r *Runner) exec(ctx context.Context, jobID int64) {
	j, _, err := r.st.JobByID(ctx, jobID)
	if err != nil {
		return
	}
	siteID := j.SiteID
	deep := j.Deep
	site, err := r.st.SiteByID(ctx, siteID)
	if err != nil {
		r.st.JobState(ctx, jobID, "failed", "site gone: "+err.Error())
		return
	}
	r.st.JobState(ctx, jobID, "running", fmt.Sprintf("scan %s", site.Key))
	progress := func(msg string) {
		r.st.JobDetail(ctx, jobID, msg)
	}
	result, runID, err := RunSiteScan(ctx, r.st, r.cfg, site, progress, deep)
	if err != nil {
		r.st.JobState(ctx, jobID, "failed", err.Error())
		return
	}
	r.st.SetJobRun(ctx, jobID, runID)
	r.st.JobState(ctx, jobID, "done",
		fmt.Sprintf("run %d: LCP %s ms", runID, metricSummary(result)))
}

// RunSiteScan is THE scan path — chrome, metrics, HTTP checks, store.
// Shared by CLI scan, manual API scan-now, and scheduled jobs.
func RunSiteScan(ctx context.Context, st *store.Store, cfg config.Config,
	site *store.Site, progress func(string), deepMode bool) (*measure.Result, int64, error) {
	bin, ver, err := chrome.Find(cfg.BrowserPath)
	if err != nil {
		return nil, 0, err
	}
	scanCtx, cancel := context.WithTimeout(ctx, cfg.ScanTimeout)
	defer cancel()
	progress("launching chrome")
	browser, stop, err := chrome.Launch(scanCtx, bin)
	if err != nil {
		return nil, 0, err
	}
	defer stop()

	started := time.Now()
	progress(fmt.Sprintf("loading %s x%d", site.URL, cfg.Samples))
	res, err := measure.Run(browser, site.URL, cfg.Samples, 1500*time.Millisecond)
	if err != nil {
		return nil, 0, err
	}
	if res.BrowserVersion == "" {
		res.BrowserVersion = ver
	}
	progress("http checks")
	if hc, herr := httpcheck.Probe(scanCtx, res.URL); herr == nil {
		var assets []httpcheck.Asset
		if n := len(res.Samples); n > 0 {
			for _, rsp := range res.Samples[n-1].Responses {
				assets = append(assets, httpcheck.Asset{URL: rsp.URL, Status: rsp.Status, Headers: rsp.Headers})
			}
		}
		hc.Cache = httpcheck.CacheVerdicts(assets)
		res.HTTP = hc
		progress("h3 probe")
		res.HTTP3 = http3.Probe(scanCtx, res.URL, hc.AltSvc)
	}
	if deepMode {
		progress("web-vitals attribution")
		if a, aerr := deep.CollectAttribution(browser, res.URL); aerr == nil {
			res.Attribution = a
		}
		if bin, berr := deep.FindBin(); berr == nil {
			progress("lighthouse")
			if lh, lerr := deep.Lighthouse(scanCtx, bin, res.URL); lerr == nil {
				res.Lighthouse = lh
			}
		}
	}
	progress("storing")
	runID, err := Persist(ctx, st, site, res, started)
	return res, runID, err
}

// Persist writes a measurement as a run row under site.
func Persist(ctx context.Context, st *store.Store, site *store.Site,
	res *measure.Result, started time.Time) (int64, error) {
	last := res.Samples[len(res.Samples)-1]
	detail, _ := json.Marshal(res)
	var bytes int64
	prot := ""
	for _, r := range last.Responses {
		bytes += r.EncodedBytes
	}
	if len(last.Responses) > 0 {
		prot = last.Responses[0].Protocol
	}
	var ttfb, cls, tbt float64
	n := float64(len(res.Samples))
	for _, s := range res.Samples {
		ttfb += s.TTFBms
		cls += s.CLS
		tbt += s.TBTms
	}
	run := &store.Run{
		SiteID: site.ID, Engine: "cdp", Trigger: "manual", Status: "ok",
		StartedAt: started, FinishedAt: time.Now(),
		BrowserVersion: res.BrowserVersion,
		TTFBms:         ttfb / n, FCPms: last.FCPms, LCPms: last.LCPms,
		CLS: cls / n, TBTms: tbt / n,
		RequestCount: len(last.Responses), PageBytes: bytes,
		HTTPProtocol: prot, DetailJSON: string(detail),
	}
	return st.InsertRun(ctx, run)
}

func mapS(in []measure.Sample, f func(measure.Sample) float64) []float64 {
	out := make([]float64, len(in))
	for i, s := range in {
		out[i] = f(s)
	}
	return out
}

func mapP(in []measure.Sample, f func(measure.Sample) *float64) []*float64 {
	out := make([]*float64, len(in))
	for i, s := range in {
		out[i] = f(s)
	}
	return out
}

func medF(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	return s[len(s)/2]
}

func medP(v []*float64) *float64 {
	var vals []float64
	for _, p := range v {
		if p != nil {
			vals = append(vals, *p)
		}
	}
	if len(vals) == 0 {
		return nil
	}
	m := medF(vals)
	return &m
}

func metricSummary(res *measure.Result) string {
	last := res.Samples[len(res.Samples)-1]
	if last.LCPms != nil {
		return fmt.Sprintf("%.0f", *last.LCPms)
	}
	return "-"
}
