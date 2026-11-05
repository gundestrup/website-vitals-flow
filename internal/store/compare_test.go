package store

import "testing"

func run(ttfb, lcp, cls float64) *Run {
	return &Run{TTFBms: ttfb, LCPms: &lcp, CLS: cls}
}

func TestCompareRuns(t *testing.T) {
	base := run(100, 500, 0.05)
	latest := run(150, 800, 0.40)
	d := CompareRuns(base, latest)
	byMetric := map[string]Delta{}
	for _, x := range d {
		byMetric[x.Metric] = x
	}
	if byMetric["ttfb_ms"].Diff != 50 || byMetric["ttfb_ms"].Pct != 50 {
		t.Fatalf("ttfb delta wrong: %+v", byMetric["ttfb_ms"])
	}
	if byMetric["lcp_ms"].Pct != 60 {
		t.Fatalf("lcp pct wrong: %+v", byMetric["lcp_ms"])
	}
	if !byMetric["cls"].Worsened(10) {
		t.Fatal("cls regression should flag")
	}
	if byMetric["lcp_ms"].Worsened(70) {
		t.Fatal("lcp +60% should NOT flag at 70% threshold")
	}
}
