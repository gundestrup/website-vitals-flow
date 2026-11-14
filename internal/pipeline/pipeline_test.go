package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gundestrup/website-vitals-flow/internal/measure"
)

func TestEngineErrors(t *testing.T) {
	var warned string
	eng := &EngineErrors{Warn: func(f string, _ ...any) {
		warned = f
	}}
	eng.Add("http_error", "http checks failed", errForTest{})
	list := eng.List()
	if len(list) != 1 || list[0].ID != "http_error" ||
		list[0].Severity != measure.SevInfo || list[0].Category != "engine" {
		t.Fatalf("engine finding: %+v", list)
	}
	if warned == "" {
		t.Fatal("warn sink should fire")
	}
	// nil Warn must not panic
	silent := &EngineErrors{}
	silent.Add("x", "y", errForTest{})
	if len(silent.List()) != 1 {
		t.Fatal("silent engine errors still record")
	}
}

type errForTest struct{}

func (errForTest) Error() string { return "boom" }

func TestMergeFindings(t *testing.T) {
	res := &measure.Result{URL: "http://dup.test",
		Samples: []measure.Sample{{TTFBms: 3000}}}
	eng := &EngineErrors{}
	eng.Add("lh_error", "lighthouse failed", errForTest{})
	MergeFindings(res, nil, eng)
	// ttfb finding (engine "" prefixed ids are ours) + engine error finding
	var ttfb, engF bool
	for _, f := range res.Findings {
		ttfb = ttfb || f.ID == "ttfb"
		engF = engF || f.ID == "lh_error"
	}
	if !ttfb || !engF {
		t.Fatalf("want ttfb + engine findings: %v", res.Findings)
	}
	// only-set filters our findings but engine errors pass through
	res2 := &measure.Result{URL: "http://dup.test",
		Samples: []measure.Sample{{TTFBms: 3000}}}
	MergeFindings(res2, map[string]bool{"lcp": true}, eng)
	for _, f := range res2.Findings {
		if f.ID == "ttfb" {
			t.Fatal("only=lcp must filter ttfb")
		}
	}
}

func TestHTTPChecks(t *testing.T) {
	var steps []string
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if ae := r.Header.Get("Accept-Encoding"); ae != "" && ae != "identity" {
			w.Header().Set("Content-Encoding", ae)
		}
		_, _ = w.Write([]byte("ok"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	res := &measure.Result{URL: srv.URL, Samples: []measure.Sample{{
		Responses: []measure.Response{{
			URL: srv.URL + "/a.css", Status: 200,
			Headers: map[string]string{"Cache-Control": "public, max-age=60"},
		}},
	}}}
	eng := &EngineErrors{}
	HTTPChecks(context.Background(), res, eng, func(s string) {
		steps = append(steps, s)
	})
	if res.HTTP == nil || res.HTTP.Status != 200 {
		t.Fatalf("http result: %+v", res.HTTP)
	}
	if len(res.HTTP.Cache) != 1 || !res.HTTP.Cache[0].Cacheable {
		t.Fatalf("cache verdicts joined: %+v", res.HTTP.Cache)
	}
	if res.HTTP3 == nil || res.HTTP3.Probed {
		t.Fatalf("http:// target must not QUIC-probe: %+v", res.HTTP3)
	}
	if len(eng.List()) != 0 {
		t.Fatalf("no engine errors expected: %v", eng.List())
	}
	if len(steps) == 0 {
		t.Fatal("progress steps not delivered")
	}
}

func TestHTTPChecksUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	res := &measure.Result{URL: url}
	eng := &EngineErrors{}
	HTTPChecks(context.Background(), res, eng, nil)
	if len(eng.List()) != 1 || eng.List()[0].ID != "http_error" {
		t.Fatalf("want http_error engine finding: %v", eng.List())
	}
}

func TestPSICheckCancelled(t *testing.T) {
	// cancelled ctx makes the outbound call fail locally — deterministic
	// without depending on whether the test env can reach googleapis.com.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := &measure.Result{URL: "https://example.com"}
	var warned string
	eng := &EngineErrors{Warn: func(f string, _ ...any) { warned = f }}
	PSICheck(ctx, res, "k", eng)
	if res.PSI != nil {
		t.Fatalf("psi must stay nil on fetch failure: %+v", res.PSI)
	}
	if !strings.Contains(warned, "psi") {
		t.Fatalf("want psi warn, got %q", warned)
	}
}
