// Package pipeline holds the scan-stage orchestration shared by the
// cli scan command and the jobs runner — one implementation, two
// call sites.
package pipeline

import (
	"context"

	"github.com/gundestrup/website-vitals-flow/internal/deep"
	"github.com/gundestrup/website-vitals-flow/internal/findings"
	"github.com/gundestrup/website-vitals-flow/internal/http3"
	"github.com/gundestrup/website-vitals-flow/internal/httpcheck"
	"github.com/gundestrup/website-vitals-flow/internal/measure"
	"github.com/gundestrup/website-vitals-flow/internal/psi"
)

// EngineErrors collects engine-layer failures as info findings —
// a missing engine never fails the scan. Warn is an optional sink
// for user-visible warnings (cli wires stderr); nil = silent.
type EngineErrors struct {
	list []measure.Finding
	Warn func(format string, a ...any)
}

// Add records an engine failure as an info finding + optional warn.
func (e *EngineErrors) Add(id, title string, err error) {
	e.list = append(e.list, measure.Finding{ID: id,
		Severity: measure.SevInfo, Category: "engine",
		Title: title, Detail: err.Error()})
	e.warnf("warn: %s: %v\n", title, err)
}

// List returns the collected engine-error findings.
func (e *EngineErrors) List() []measure.Finding { return e.list }

func (e *EngineErrors) warnf(f string, a ...any) {
	if e.Warn != nil {
		e.Warn(f, a...)
	}
}

func step(progress func(string), s string) {
	if progress != nil {
		progress(s)
	}
}

// HTTPChecks probes h1/h2, joins cache verdicts from the last
// sample's responses, then runs the h3 probe guided by Alt-Svc.
func HTTPChecks(ctx context.Context, res *measure.Result,
	eng *EngineErrors, progress func(string)) {
	hc, herr := httpcheck.Probe(ctx, res.URL)
	if herr != nil {
		eng.Add("http_error", "http checks failed", herr)
		return
	}
	var assets []httpcheck.Asset
	if n := len(res.Samples); n > 0 {
		for _, rsp := range res.Samples[n-1].Responses {
			assets = append(assets, httpcheck.Asset{
				URL: rsp.URL, Status: rsp.Status, Headers: rsp.Headers})
		}
	}
	hc.Cache = httpcheck.CacheVerdicts(assets)
	res.HTTP = hc
	step(progress, "h3 probe")
	res.HTTP3 = http3.Probe(ctx, res.URL, hc.AltSvc)
}

// DeepChecks adds web-vitals attribution + lighthouse audits,
// degrading to engine findings when either layer fails.
func DeepChecks(ctx, browser context.Context, res *measure.Result,
	eng *EngineErrors, progress func(string)) {
	step(progress, "web-vitals attribution")
	if a, frames, ff, aerr := deep.CollectAttribution(browser, res.URL); aerr == nil {
		res.Attribution = a
		res.Filmstrip = frames
		res.FilmstripFormat = ff
	} else {
		eng.Add("attr_error", "attribution collect failed", aerr)
	}
	if bin, berr := deep.FindBin(); berr == nil {
		step(progress, "lighthouse")
		if lh, lerr := deep.Lighthouse(ctx, bin, res.URL); lerr == nil {
			res.Lighthouse = lh
		} else {
			eng.Add("lh_error", "lighthouse audit failed", lerr)
		}
	} else {
		eng.warnf("warn: lighthouse not on PATH — skipping audits\n")
	}
}

// PSICheck attaches a PageSpeed Insights result when the API answers.
func PSICheck(ctx context.Context, res *measure.Result, key string, eng *EngineErrors) {
	if r, perr := psi.Fetch(ctx, res.URL, key); perr == nil {
		res.PSI = r
	} else {
		eng.warnf("warn: psi: %v\n", perr)
	}
}

// MergeFindings analyzes the result, merges engine-derived findings
// and engine errors, then dedups.
func MergeFindings(res *measure.Result, only map[string]bool, eng *EngineErrors) {
	res.Findings = findings.Filter(findings.Analyze(res), only)
	res.Findings = append(res.Findings, findings.FromLighthouse(res.Lighthouse)...)
	res.Findings = append(res.Findings, findings.FromAttribution(res.Attribution)...)
	res.Findings = append(res.Findings, findings.FromPSI(res.PSI)...)
	res.Findings = append(res.Findings, eng.List()...)
	res.Findings = findings.Dedup(res.Findings)
}
