package store

import "fmt"

// Delta is one metric's change between baseline and latest run.
type Delta struct {
	Metric string
	Base   float64
	Latest float64
	Diff   float64 // latest - base
	Pct    float64 // % change; + worse for time metrics
}

// CompareRuns diffs headline metrics between two runs.
func CompareRuns(base, latest *Run) []Delta {
	var out []Delta
	add := func(name string, b, l float64) {
		pct := 0.0
		if b != 0 {
			pct = (l - b) / b * 100
		}
		out = append(out, Delta{Metric: name, Base: b, Latest: l,
			Diff: l - b, Pct: pct})
	}
	add("ttfb_ms", base.TTFBms, latest.TTFBms)
	if base.FCPms != nil && latest.FCPms != nil {
		add("fcp_ms", *base.FCPms, *latest.FCPms)
	}
	if base.LCPms != nil && latest.LCPms != nil {
		add("lcp_ms", *base.LCPms, *latest.LCPms)
	}
	add("cls", base.CLS, latest.CLS)
	add("tbt_ms", base.TBTms, latest.TBTms)
	add("requests", float64(base.RequestCount), float64(latest.RequestCount))
	add("bytes", float64(base.PageBytes), float64(latest.PageBytes))
	return out
}

// Worsened reports deltas that regressed beyond a % threshold.
func (d Delta) Worsened(threshPct float64) bool {
	return d.Pct > threshPct && d.Diff > 0
}

// String formats a delta for CLI output.
func (d Delta) String() string {
	return fmt.Sprintf("%-10s base %.1f → latest %.1f (%+.1f, %+.0f%%)",
		d.Metric, d.Base, d.Latest, d.Diff, d.Pct)
}
