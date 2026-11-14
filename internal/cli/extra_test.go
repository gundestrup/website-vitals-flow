package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gundestrup/website-vitals-flow/internal/chrome"
	"github.com/gundestrup/website-vitals-flow/internal/config"
	"github.com/gundestrup/website-vitals-flow/internal/measure"
	"github.com/gundestrup/website-vitals-flow/internal/store"
)

func TestVersionCommand(_ *testing.T) {
	versionCmd().Run(nil, nil) // prints "wvf <version>" — must not panic
}

func TestDoctorCommand(t *testing.T) {
	chdirTemp(t)
	if err := doctorCmd().RunE(nil, nil); err != nil {
		t.Fatalf("doctor: %v", err)
	}
}

func TestScanBadBrowserPath(t *testing.T) {
	dir := chdirTemp(t)
	if err := os.WriteFile(dir+"/wvf.yml",
		[]byte("browser_path: /nonexistent/wvf-test-browser\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Execute() (not RunE) so cobra installs a non-nil cmd.Context —
	// RunE derives the scan timeout ctx from it. --config is required:
	// Load("") returns defaults; wvf.yml is only read via the flag.
	cmd := scanCmd()
	cmd.SetArgs([]string{"--config", dir + "/wvf.yml", "http://x.test"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("want browser_path error")
	} else if !strings.Contains(err.Error(), "browser_path") {
		t.Fatalf("error should name the cause: %v", err)
	}
}

func TestServeBadListenAddr(t *testing.T) {
	dir := chdirTemp(t)
	if err := os.WriteFile(dir+"/wvf.yml",
		[]byte("listen: \"bad addr\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// SplitHostPort("bad addr") fails inside replaceStaleServe → no
	// lsof/kill of a real :8420 listener; ListenAndServe then errors.
	cmd := serveCmd()
	cmd.SetArgs([]string{"--config", dir + "/wvf.yml"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("want listen error")
	}
}

func TestMCPCommandStdinEOF(t *testing.T) {
	chdirTemp(t)
	// mcp serves until stdin EOF — skip when the test runner hands us a
	// terminal (interactive go test) where EOF never comes.
	if fi, _ := os.Stdin.Stat(); fi.Mode()&os.ModeCharDevice != 0 {
		t.Skip("stdin is a terminal — Serve would block")
	}
	cmd := mcpCmd()
	done := make(chan error, 1)
	go func() { done <- cmd.RunE(cmd, nil) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("mcp: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("mcp did not exit on stdin EOF")
	}
}

// emitResult + persist are the post-measure halves of the scan path —
// deterministic against a fabricated Result.
func TestEmitResultAndPersist(t *testing.T) {
	dir := chdirTemp(t)
	res := &measure.Result{URL: "http://emit.test", BrowserVersion: "c-1",
		Samples: []measure.Sample{{
			TTFBms: 120, CLS: 0.02, TBTms: 30, DOMNodes: 400,
			Responses: []measure.Response{{URL: "http://emit.test/", Protocol: "h2",
				Status: 200, EncodedBytes: 900}},
		}},
		Findings: []measure.Finding{{ID: "x", Severity: "warn", Title: "t", Link: "l"}},
	}
	if err := emitResult(res, false, dir+"/out/report.html"); err != nil {
		t.Fatalf("emitResult html: %v", err)
	}
	if _, err := os.Stat(dir + "/out/report.html"); err != nil {
		t.Fatal("html report not written")
	}
	if err := emitResult(res, true, ""); err != nil {
		t.Fatalf("emitResult json: %v", err)
	}
	// empty-samples edge
	if err := emitResult(&measure.Result{}, false, ""); err != nil {
		t.Fatalf("empty result: %v", err)
	}
	if err := persist(context.Background(), cfgForTest(dir), res, time.Now()); err != nil {
		t.Fatalf("persist: %v", err)
	}
	st, err := store.Open(context.Background(), dir+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	site, err := st.SiteByKey(context.Background(), "emit.test")
	if err != nil {
		t.Fatalf("scan should auto-create site: %v", err)
	}
	runs, _ := st.RunsForSite(context.Background(), site.ID, 5)
	if len(runs) != 1 {
		t.Fatalf("want 1 persisted run, got %d", len(runs))
	}
	_ = st.Close()
}

func cfgForTest(dir string) config.Config {
	return config.Config{
		StateDB: dir + "/state.db", KeepLast: 100, MaxAgeDays: 56}
}

// TestScanIntegration exercises the real scan path — chrome launch,
// measure.Run, pipeline checks, persist — against a local server.
// Skips when no Chrome/Chromium is available.
func TestScanIntegration(t *testing.T) {
	if _, _, err := chrome.Find(""); err != nil {
		t.Skipf("no chrome available: %v", err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html><body><h1>hi</h1><img src="/i.png"></body></html>`))
	})
	mux.HandleFunc("/i.png", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, 2048))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := chdirTemp(t)
	cmd := scanCmd()
	cmd.SetArgs([]string{srv.URL, "--samples", "1", "--settle", "300ms", "--timeout", "60s"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	st, err := store.Open(context.Background(), dir+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	sites, _ := st.ListSites(context.Background())
	if len(sites) != 1 {
		t.Fatalf("scan should create one site, got %d", len(sites))
	}
	runs, _ := st.RunsForSite(context.Background(), sites[0].ID, 5)
	if len(runs) != 1 || runs[0].Status != "ok" {
		t.Fatalf("want 1 ok run: %+v", runs)
	}
	if runs[0].RequestCount < 2 {
		t.Fatalf("page + image expected, got %d requests", runs[0].RequestCount)
	}
}
