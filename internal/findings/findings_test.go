package findings

import (
	"strings"
	"testing"

	"github.com/gundestrup/website-vitals-flow/internal/deep"
	"github.com/gundestrup/website-vitals-flow/internal/http3"
	"github.com/gundestrup/website-vitals-flow/internal/httpcheck"
	"github.com/gundestrup/website-vitals-flow/internal/measure"
	"github.com/gundestrup/website-vitals-flow/internal/psi"
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

func byID(fs []Finding, id string) *Finding {
	for i := range fs {
		if fs[i].ID == id {
			return &fs[i]
		}
	}
	return nil
}

func f64(v float64) *float64 { return &v }

// oneResult wraps a single sample into a minimal Result.
func oneResult(s measure.Sample) *measure.Result {
	return &measure.Result{Samples: []measure.Sample{s}}
}

func TestVitalThresholds(t *testing.T) {
	for _, c := range []struct {
		name string
		mut  func(*measure.Sample)
		id   string
		sev  Severity // "" = no finding
	}{
		{"lcp good", func(s *measure.Sample) { s.LCPms = f64(2400) }, "lcp", ""},
		{"lcp warn", func(s *measure.Sample) { s.LCPms = f64(3000) }, "lcp", Warn},
		{"lcp poor", func(s *measure.Sample) { s.LCPms = f64(4500) }, "lcp", Poor},
		{"lcp nil", func(_ *measure.Sample) {}, "lcp", ""},
		{"tbt warn", func(s *measure.Sample) { s.TBTms = 300 }, "tbt", Warn},
		{"tbt poor", func(s *measure.Sample) { s.TBTms = 700 }, "tbt", Poor},
		{"ttfb warn", func(s *measure.Sample) { s.TTFBms = 1000 }, "ttfb", Warn},
		{"ttfb poor", func(s *measure.Sample) { s.TTFBms = 2000 }, "ttfb", Poor},
		{"cls good", func(s *measure.Sample) { s.CLS = 0.05 }, "cls", ""},
		{"cls warn", func(s *measure.Sample) { s.CLS = 0.15 }, "cls", Warn},
		{"cls poor", func(s *measure.Sample) { s.CLS = 0.3 }, "cls", Poor},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := measure.Sample{}
			c.mut(&s)
			f := byID(Analyze(oneResult(s)), c.id)
			if c.sev == "" {
				if f != nil {
					t.Fatalf("unexpected %s finding: %+v", c.id, f)
				}
				return
			}
			if f == nil || f.Severity != c.sev {
				t.Fatalf("want %s %s, got %+v", c.id, c.sev, f)
			}
			if f.Link == "" {
				t.Fatalf("%s finding missing doc link", c.id)
			}
		})
	}
}

func TestCLSAttributionDetail(t *testing.T) {
	res := oneResult(measure.Sample{CLS: 0.3})
	res.Attribution = &deep.Attribution{CLSItems: []string{"div.hero"}}
	f := byID(Analyze(res), "cls")
	if f == nil || !strings.Contains(f.Detail, "div.hero") {
		t.Fatalf("cls detail should name shift source: %+v", f)
	}
}

func TestCompressionFindings(t *testing.T) {
	res := oneResult(measure.Sample{Responses: []measure.Response{
		{URL: "http://x/app.css", MimeType: "text/css", EncodedBytes: 5000},
		{URL: "http://x/tiny.css", MimeType: "text/css", EncodedBytes: 100},
		{URL: "http://x/ok.js", MimeType: "text/javascript",
			EncodedBytes: 5000, ContentEncoding: "br"},
	}})
	f := byID(Analyze(res), "encoding")
	if f == nil || !strings.Contains(f.Detail, "app.css") {
		t.Fatalf("want encoding finding naming app.css: %+v", f)
	}
}

func TestBrotliHint(t *testing.T) {
	res := oneResult(measure.Sample{})
	res.HTTP = &httpcheck.Result{Encodings: map[string]*httpcheck.EncodingProbe{
		"br": {Offered: "br", Returned: ""},
	}}
	if byID(Analyze(res), "brotli") == nil {
		t.Fatal("want brotli info finding when br not negotiated")
	}
	res.HTTP.Encodings["br"].Returned = "br"
	if byID(Analyze(res), "brotli") != nil {
		t.Fatal("negotiated br must not flag")
	}
}

func TestCacheFindings(t *testing.T) {
	res := oneResult(measure.Sample{})
	res.HTTP = &httpcheck.Result{Cache: []httpcheck.CacheVerdict{
		{URL: "http://x/app.css", Cacheable: false, Reasons: []string{"no-cache"}},
		{URL: "http://x/api/data", Cacheable: false}, // non-static → ignored
	}}
	f := byID(Analyze(res), "cache")
	if f == nil || !strings.Contains(f.Detail, "app.css") {
		t.Fatalf("want cache finding for static asset: %+v", f)
	}
}

func TestProtocolFindings(t *testing.T) {
	res := oneResult(measure.Sample{Responses: []measure.Response{
		{URL: "http://x/", Protocol: "http/1.1"}}})
	if byID(Analyze(res), "h2") == nil {
		t.Fatal("http/1.1 document should flag h2")
	}
	res.Samples[0].Responses[0].Protocol = "h2"
	if byID(Analyze(res), "h2") != nil {
		t.Fatal("h2 document must not flag")
	}

	res = oneResult(measure.Sample{})
	res.URL = "https://x.test"
	res.HTTP = &httpcheck.Result{TLSVersion: "TLS 1.3"}
	res.HTTP3 = &http3.Result{Advertised: false}
	if byID(Analyze(res), "h3") == nil {
		t.Fatal("TLS site without alt-svc h3 should flag h3")
	}
	res.HTTP3.Advertised = true
	if byID(Analyze(res), "h3") != nil {
		t.Fatal("advertised h3 must not flag")
	}
}

func TestTLSFinding(t *testing.T) {
	res := oneResult(measure.Sample{})
	res.URL = "http://shop.example.com"
	if byID(Analyze(res), "tls") == nil {
		t.Fatal("remote http:// site should flag tls")
	}
	for _, u := range []string{"http://localhost:3000", "http://127.0.0.1",
		"https://shop.example.com"} {
		res.URL = u
		if byID(Analyze(res), "tls") != nil {
			t.Fatalf("%s must not flag tls", u)
		}
	}
}

func TestImageFindings(t *testing.T) {
	res := oneResult(measure.Sample{Images: []measure.ImageInfo{{
		Src: "http://x/hero.jpg", NatW: 2000, NatH: 1000,
		DispW: 500, DispH: 250, Bytes: 100 << 10, Format: "jpeg",
	}}})
	fs := Analyze(res)
	if byID(fs, "img_oversize") == nil || byID(fs, "img_format") == nil {
		t.Fatalf("want both image findings: %v", fs)
	}
	// right-sized modern format → neither
	res.Samples[0].Images[0] = measure.ImageInfo{
		Src: "http://x/ok.webp", NatW: 500, NatH: 250,
		DispW: 500, DispH: 250, Bytes: 30 << 10, Format: "webp",
	}
	fs = Analyze(res)
	if byID(fs, "img_oversize") != nil || byID(fs, "img_format") != nil {
		t.Fatalf("right-sized webp must not flag: %v", fs)
	}
}

func TestBudgetFindings(t *testing.T) {
	many := make([]measure.Response, 76)
	big := make([]measure.Response, 1)
	big[0] = measure.Response{URL: "http://x/a.bin", EncodedBytes: 3 << 20}
	res := oneResult(measure.Sample{Responses: many})
	if byID(Analyze(res), "requests") == nil {
		t.Fatal("76 requests should flag")
	}
	res.Samples[0].Responses = big
	if byID(Analyze(res), "weight") == nil {
		t.Fatal(">2MB page weight should flag")
	}
	res = oneResult(measure.Sample{DOMNodes: 1600, JSHeap: 33 << 20})
	fs := Analyze(res)
	if byID(fs, "domsize") == nil || byID(fs, "jsheap") == nil {
		t.Fatalf("want domsize+jsheap: %v", fs)
	}
}

func TestJSCSSBudgets(t *testing.T) {
	resps := []measure.Response{
		{URL: "http://x/big.js", MimeType: "text/javascript", EncodedBytes: 600_000},
		{URL: "http://x/more.js", MimeType: "text/javascript", EncodedBytes: 1_000_000},
		{URL: "http://x/huge.css", MimeType: "text/css", EncodedBytes: 250_000},
		{URL: "http://x/extra.css", MimeType: "text/css", EncodedBytes: 100_000},
	}
	res := oneResult(measure.Sample{Responses: resps})
	ids := map[string]bool{}
	for _, f := range Analyze(res) {
		ids[f.ID] = true
	}
	for _, id := range []string{"js_weight", "js_bundle", "css_weight", "css_bundle"} {
		if !ids[id] {
			t.Fatalf("missing %s in %v", id, ids)
		}
	}
	// >15 JS files → js_count
	resps = nil
	for i := 0; i < 16; i++ {
		resps = append(resps, measure.Response{
			URL: "http://x/f.js", MimeType: "text/javascript", EncodedBytes: 100})
	}
	res = oneResult(measure.Sample{Responses: resps})
	if byID(Analyze(res), "js_count") == nil {
		t.Fatal("16 JS files should flag js_count")
	}
}

func TestBasicsFindings(t *testing.T) {
	res := oneResult(measure.Sample{})
	res.HTTP = &httpcheck.Result{Basics: map[string]int{
		"/robots.txt": 404, "/favicon.ico": 200, "/sitemap.xml": 0,
	}}
	fs := Analyze(res)
	if byID(fs, "basics_robots.txt") == nil || byID(fs, "basics_sitemap.xml") == nil {
		t.Fatalf("want missing-file basics findings: %v", fs)
	}
	if byID(fs, "basics_favicon.ico") != nil {
		t.Fatal("200 favicon must not flag")
	}
}

func TestSeverityOrdering(t *testing.T) {
	res := oneResult(measure.Sample{
		CLS: 0.3, // poor
		Responses: []measure.Response{
			{URL: "http://x/", Protocol: "http/1.1"}, // h2 info
		},
	})
	fs := Analyze(res)
	if len(fs) < 2 || fs[0].Severity != Poor {
		t.Fatalf("poor findings must sort first: %v", fs)
	}
}

func TestParseOnlyWantFilter(t *testing.T) {
	if ParseOnly("") != nil {
		t.Fatal("empty only should be nil")
	}
	only := ParseOnly("lcp, cls")
	if !only["lcp"] || !only["cls"] || only["http"] {
		t.Fatalf("parse: %v", only)
	}
	if !Want(nil, "http") || !Want(map[string]bool{"deep": true}, "http") {
		t.Fatal("nil/deep-only set must run everything")
	}
	if Want(only, "http") || !Want(only, "lcp") {
		t.Fatal("only-set must gate stages")
	}
	fs := []Finding{
		{ID: "lcp", Category: "vitals"},
		{ID: "h2", Category: "protocol"},
	}
	if got := Filter(fs, nil); len(got) != 2 {
		t.Fatal("nil filter keeps all")
	}
	if got := Filter(fs, map[string]bool{"vitals": true}); len(got) != 1 ||
		got[0].ID != "lcp" {
		t.Fatalf("category filter: %v", got)
	}
	if got := Filter(fs, map[string]bool{"h2": true}); len(got) != 1 ||
		got[0].ID != "h2" {
		t.Fatalf("id filter: %v", got)
	}
}

func TestDedup(t *testing.T) {
	fs := []Finding{
		{ID: "a", Title: "t", Detail: "d"},
		{ID: "a", Title: "t", Detail: "d"},
		{ID: "a", Title: "t2", Detail: "d"},
	}
	if got := Dedup(fs); len(got) != 2 {
		t.Fatalf("dedup: %v", got)
	}
}

func TestFromLighthouse(t *testing.T) {
	if FromLighthouse(nil) != nil {
		t.Fatal("nil report → nil")
	}
	fs := FromLighthouse(&deep.LHReport{Findings: []deep.LHFinding{
		{ID: "x", Title: "bad", Score: 0.3, Display: "1.2s",
			Savings: 800, SavingsB: 51200, Items: []string{"a.js"}, Link: "L"},
		{ID: "y", Title: "meh", Score: 0.7},
	}})
	if len(fs) != 2 {
		t.Fatalf("want 2 findings: %v", fs)
	}
	if fs[0].ID != "lh_x" || fs[0].Severity != Poor ||
		!strings.Contains(fs[0].Detail, "~800ms") ||
		!strings.Contains(fs[0].Detail, "~50KB") ||
		!strings.Contains(fs[0].Detail, "a.js") {
		t.Fatalf("lh finding shape: %+v", fs[0])
	}
	if fs[1].Severity != Warn {
		t.Fatalf("score 0.7 → warn, got %s", fs[1].Severity)
	}
}

func TestFromAttribution(t *testing.T) {
	if FromAttribution(nil) != nil {
		t.Fatal("nil attribution → nil")
	}
	fs := FromAttribution(&deep.Attribution{
		LCPElement: "IMG", LCPURL: "http://x/hero.jpg",
		LCPSubparts: []string{"ttfb=100"},
		CLSItems:    []string{"div.a", "div.b"},
		INPms:       f64(320), INPType: "click", INPTarget: "button",
	})
	ids := map[string]int{}
	for _, f := range fs {
		ids[f.ID]++
	}
	if ids["attr_lcp"] != 1 || ids["attr_lcp_parts"] != 1 ||
		ids["attr_cls"] != 2 || ids["attr_inp"] != 1 {
		t.Fatalf("attr ids: %v", ids)
	}
	for _, f := range fs {
		if f.Severity != Info || f.Category != "attribution" {
			t.Fatalf("attr findings are info/attribution: %+v", f)
		}
	}
}

func TestFromPSI(t *testing.T) {
	if FromPSI(nil) != nil {
		t.Fatal("nil result → nil")
	}
	fs := FromPSI(&psi.Result{
		FieldData: true,
		P75: map[string]float64{
			"LARGEST_CONTENTFUL_PAINT_MS":   5000, // poor
			"CUMULATIVE_LAYOUT_SHIFT_SCORE": 30,   // 0.30 → poor
		},
		PerfScore: 50,
	})
	if byID(fs, "psi_lcp") == nil || byID(fs, "psi_lcp").Severity != Poor {
		t.Fatalf("want poor psi_lcp: %v", fs)
	}
	if byID(fs, "psi_cls") == nil || byID(fs, "psi_score") == nil {
		t.Fatalf("want psi_cls + psi_score: %v", fs)
	}
	// good field data + high score → nothing
	fs = FromPSI(&psi.Result{FieldData: true,
		P75:       map[string]float64{"LARGEST_CONTENTFUL_PAINT_MS": 2000},
		PerfScore: 95})
	if len(fs) != 1 || fs[0].Severity != Info {
		t.Fatalf("good p75 yields info finding only: %v", fs)
	}
}
