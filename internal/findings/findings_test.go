package findings

import (
	"testing"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/measure"
)

// Regression: a scan with no http-check data must not panic —
// this killed the serve process (res.HTTP nil deref).
func TestAnalyzeNilHTTP(t *testing.T) {
	res := &measure.Result{
		Samples: []measure.Sample{{TTFBms: 100, CLS: 0.3}},
	}
	fs := Analyze(res)
	if fs == nil {
		t.Fatal("want findings for the cls=0.3 sample")
	}
}

// Regression: an all-failed/empty sample set must not panic on
// Samples[len-1].
func TestAnalyzeEmptySamples(t *testing.T) {
	res := &measure.Result{}
	fs := Analyze(res)
	if len(fs) != 0 {
		t.Fatalf("want no findings, got %v", fs)
	}
}

func TestAnalyzeFindingsPresent(t *testing.T) {
	res := &measure.Result{
		Samples: []measure.Sample{{
			TTFBms: 100, CLS: 0.3,
			Responses: []measure.Response{{
				URL: "http://x/app.js", MimeType: "text/javascript",
				EncodedBytes: 900_000,
			}},
		}},
	}
	ids := map[string]bool{}
	for _, f := range Analyze(res) {
		ids[f.ID] = true
	}
	if !ids["cls"] || !ids["js_bundle"] {
		t.Fatalf("missing expected findings: %v", ids)
	}
}
