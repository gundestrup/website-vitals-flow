package jobs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gundestrup/website-vitals-flow/internal/chrome"
	"github.com/gundestrup/website-vitals-flow/internal/config"
	"github.com/gundestrup/website-vitals-flow/internal/measure"
	"github.com/gundestrup/website-vitals-flow/internal/store"
)

func openTemp(t *testing.T) (*store.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir()+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, ctx
}

func TestPersist(t *testing.T) {
	st, ctx := openTemp(t)
	site, err := st.GetOrCreateSite(ctx, "p.test", "http://p.test", 10, 7)
	if err != nil {
		t.Fatal(err)
	}
	lcp := 1234.0
	res := &measure.Result{BrowserVersion: "c-1", Samples: []measure.Sample{
		{TTFBms: 100, CLS: 0.1, TBTms: 50,
			Responses: []measure.Response{{Protocol: "h2", EncodedBytes: 100}}},
		{TTFBms: 200, CLS: 0.3, TBTms: 150, LCPms: &lcp,
			Responses: []measure.Response{
				{Protocol: "h2", EncodedBytes: 300},
				{Protocol: "h2", EncodedBytes: 400}}},
	}}
	started := time.Now().Add(-time.Second)
	runID, err := Persist(ctx, st, site, res, started)
	if err != nil {
		t.Fatal(err)
	}
	r, err := st.RunByID(ctx, runID)
	if err != nil {
		t.Fatal(err)
	}
	if r.TTFBms != 150 || r.CLS != 0.2 || r.TBTms != 100 {
		t.Fatalf("averaged metrics: %+v", r)
	}
	if r.RequestCount != 2 || r.PageBytes != 700 || r.HTTPProtocol != "h2" {
		t.Fatalf("page stats: %+v", r)
	}
	if r.LCPms == nil || *r.LCPms != 1234 {
		t.Fatalf("lcp: %+v", r.LCPms)
	}
	if r.Engine != "cdp" || r.Status != "ok" || r.BrowserVersion != "c-1" {
		t.Fatalf("run meta: %+v", r)
	}
}

func TestMetricSummary(t *testing.T) {
	if got := metricSummary(&measure.Result{Samples: []measure.Sample{{}}}); got != "-" {
		t.Fatalf("nil lcp → -, got %q", got)
	}
	lcp := 123.4
	res := &measure.Result{Samples: []measure.Sample{{LCPms: &lcp}}}
	if got := metricSummary(res); got != "123" {
		t.Fatalf("want 123, got %q", got)
	}
}

func TestEnqueueFailsJobWithoutBrowser(t *testing.T) {
	st, ctx := openTemp(t)
	site, err := st.GetOrCreateSite(ctx, "j.test", "http://j.test", 10, 7)
	if err != nil {
		t.Fatal(err)
	}
	// deterministic failure: stat of a nonexistent browser_path errors
	// before any chrome/launch work — exercises exec's failed path.
	r := New(st, config.Config{BrowserPath: "/nonexistent/wvf-test-chrome"})
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go r.Start(rctx)

	id, err := r.Enqueue(ctx, site.ID, "scan", false, "")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, _, err := st.JobByID(ctx, id)
		if err == nil && j.State == "failed" {
			if !strings.Contains(j.Detail, "browser_path") {
				t.Fatalf("detail should name the cause: %q", j.Detail)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("job did not reach failed state")
}

func TestEnqueuePageAndOnlyOverride(t *testing.T) {
	st, ctx := openTemp(t)
	site, err := st.GetOrCreateSite(ctx, "pg.test", "http://pg.test", 10, 7)
	if err != nil {
		t.Fatal(err)
	}
	r := New(st, config.Config{BrowserPath: "/nonexistent/wvf-test-chrome"})
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go r.Start(rctx)
	// page + only land on the job row; exec still fails at chrome.Find
	id, err := r.Enqueue(ctx, site.ID, "scan", true, "lcp,cls", "about")
	if err != nil {
		t.Fatal(err)
	}
	if page, _ := st.JobPage(ctx, id); page != "about" {
		t.Fatalf("job page: %q", page)
	}
	j, _, _ := st.JobByID(ctx, id)
	if j.Only != "lcp,cls" || !j.Deep {
		t.Fatalf("job flags: %+v", j)
	}
}

func TestQueueFull(t *testing.T) {
	st, ctx := openTemp(t)
	site, err := st.GetOrCreateSite(ctx, "q.test", "http://q.test", 10, 7)
	if err != nil {
		t.Fatal(err)
	}
	r := New(st, config.Config{})
	// no Start — the buffered queue (cap 32) must reject the 33rd offer.
	for i := 0; i < 32; i++ {
		if _, err := r.Enqueue(ctx, site.ID, "scan", false, ""); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if _, err := r.Enqueue(ctx, site.ID, "scan", false, ""); err == nil {
		t.Fatal("33rd enqueue should report queue full")
	}
}

// TestRunSiteScanIntegration runs THE shared scan path end-to-end —
// chrome launch, measure, HTTP checks, deep-mode degrade, persist —
// against a local page. Skips when no usable Chrome exists.
func TestRunSiteScanIntegration(t *testing.T) {
	bin, _, err := chrome.Find("")
	if err != nil {
		t.Skipf("no chrome: %v", err)
	}
	lctx, lcancel := context.WithTimeout(context.Background(), 15*time.Second)
	_, stop, lerr := chrome.Launch(lctx, bin)
	if lerr != nil {
		lcancel()
		t.Skipf("chrome won't launch: %v", lerr)
	}
	stop()
	lcancel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body><h1>t</h1><img src='/i.png'></body></html>"))
	}))
	defer srv.Close()

	st, ctx := openTemp(t)
	site, err := st.GetOrCreateSite(ctx, "int.test", srv.URL, 10, 7)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Samples = 1
	var prog []string
	res, runID, err := RunSiteScan(ctx, st, cfg, site,
		func(s string) { prog = append(prog, s) }, true)
	if err != nil {
		t.Fatalf("RunSiteScan: %v", err)
	}
	if res == nil || len(res.Samples) != 1 || runID == 0 {
		t.Fatalf("res=%v runID=%d", res, runID)
	}
	if len(prog) == 0 {
		t.Error("no progress callbacks")
	}
	run, err := st.RunByID(ctx, runID)
	if err != nil || run == nil {
		t.Fatalf("run not persisted: %v", err)
	}
}
