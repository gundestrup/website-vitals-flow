package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gundestrup/website-vitals-flow/internal/config"
	"github.com/gundestrup/website-vitals-flow/internal/jobs"
	"github.com/gundestrup/website-vitals-flow/internal/store"
)

func seed(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir()+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	site, _ := st.GetOrCreateSite(context.Background(), "a.test", "http://a.test", 10, 7)
	if _, err := st.InsertRun(context.Background(), &store.Run{SiteID: site.ID, Engine: "cdp",
		Status: "ok", StartedAt: time.Now(), FinishedAt: time.Now(), TTFBms: 42,
		DetailJSON: `{"findings":[{"severity":"poor","title":"cls 0.3"}]}`}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	return New(st, cfg, jobs.New(st, cfg)), st
}

func do(t *testing.T, s http.Handler, method, path, body string) (int, map[string]any, []map[string]any) {
	t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, rdr)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var obj map[string]any
	var arr []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &obj); err != nil {
		_ = json.Unmarshal(rec.Body.Bytes(), &arr)
	}
	return rec.Code, obj, arr
}

func TestAPIEndpoints(t *testing.T) {
	s, _ := seed(t)
	code, _, arr := do(t, s, "GET", "/api/sites", "")
	if code != 200 || len(arr) != 1 || arr[0]["key"] != "a.test" {
		t.Fatalf("sites: %d %v", code, arr)
	}
	code, _, arr = do(t, s, "GET", "/api/sites/a.test/runs", "")
	if code != 200 || len(arr) != 1 || arr[0]["ttfb_ms"] != 42.0 {
		t.Fatalf("runs: %d %v", code, arr)
	}
	code, obj, _ := do(t, s, "GET", "/api/runs/1", "")
	if code != 200 || obj["id"] != 1.0 {
		t.Fatalf("run: %d %v", code, obj)
	}
	code, _, _ = do(t, s, "GET", "/api/jobs", "")
	if code != 200 {
		t.Fatalf("jobs: %d", code)
	}
}

func TestSiteCRUD(t *testing.T) {
	s, _ := seed(t)
	code, obj, _ := do(t, s, "POST", "/api/sites",
		`{"url":"http://new.test:8080","label":"new","schedule":"1h","deep":true,"only":"lcp,cls"}`)
	if code != 200 || obj["key"] != "new.test:8080" || obj["deep"] != true || obj["only"] != "lcp,cls" {
		t.Fatalf("add: %d %v", code, obj)
	}
	code, obj, _ = do(t, s, "PATCH", "/api/sites/new.test:8080",
		`{"schedule":"30m","deep":false}`)
	if code != 200 || obj["schedule"] != "30m" || obj["deep"] != false {
		t.Fatalf("edit: %d %v", code, obj)
	}
	code, _, _ = do(t, s, "DELETE", "/api/sites/new.test:8080", "")
	if code != 200 {
		t.Fatalf("delete: %d", code)
	}
	code, _, arr := do(t, s, "GET", "/api/sites", "")
	if code != 200 || len(arr) != 1 {
		t.Fatalf("after delete want 1 site, got %d %v", code, arr)
	}
}

func TestScanNowPruneBadge(t *testing.T) {
	s, _ := seed(t)
	code, obj, _ := do(t, s, "POST", "/api/scan-now",
		`{"key":"a.test","deep":true,"only":"lcp"}`)
	if code != 200 || obj["job"] == nil {
		t.Fatalf("scan-now: %d %v", code, obj)
	}
	code, obj, _ = do(t, s, "POST", "/api/prune", `{"key":"a.test"}`)
	if code != 200 || obj["dropped"] == nil {
		t.Fatalf("prune: %d %v", code, obj)
	}
	req := httptest.NewRequest("GET", "/badge.svg?metric=lcp&value=900", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "svg") {
		t.Fatalf("badge: %d", rec.Code)
	}
	req = httptest.NewRequest("GET", "/site/a.test", nil)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "website-vitals-flow") ||
		!strings.Contains(rec.Body.String(), "metric-cell") {
		t.Fatalf("site route: %d", rec.Code) // server-rendered site.tmpl
	}
	req = httptest.NewRequest("GET", "/report/1", nil)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "cls 0.3") {
		t.Fatalf("report: %d %s", rec.Code, rec.Body.String()[:200])
	}
}

// mcpText pulls the JSON text out of a tools/call content response.
func mcpText(t *testing.T, obj map[string]any) string {
	t.Helper()
	res, ok := obj["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result envelope: %v", obj)
	}
	content, ok := res["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("no content: %v", res)
	}
	return content[0].(map[string]any)["text"].(string)
}

func TestMCPOverHTTP(t *testing.T) {
	s, st := seed(t)

	// initialize handshake
	code, obj, _ := do(t, s, "POST", "/mcp",
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if code != 200 {
		t.Fatalf("initialize: %d %v", code, obj)
	}
	init := obj["result"].(map[string]any)
	if init["protocolVersion"] != "2024-11-05" ||
		init["serverInfo"].(map[string]any)["name"] != "wvf" {
		t.Fatalf("initialize result: %v", init)
	}

	// tools/list — same catalog as stdio
	code, obj, _ = do(t, s, "POST", "/mcp",
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if code != 200 {
		t.Fatalf("tools/list: %d %v", code, obj)
	}
	tools := obj["result"].(map[string]any)["tools"].([]any)
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.(map[string]any)["name"].(string)] = true
	}
	for _, want := range []string{"scan", "sites", "history", "run", "jobs", "prune"} {
		if !names[want] {
			t.Fatalf("missing tool %s", want)
		}
	}

	// tools/call sites — read path through the shared dispatch
	code, obj, _ = do(t, s, "POST", "/mcp",
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"sites","arguments":{}}}`)
	var sites []map[string]any
	if err := json.Unmarshal([]byte(mcpText(t, obj)), &sites); err != nil {
		t.Fatal(err)
	}
	if code != 200 || len(sites) != 1 || sites[0]["key"] != "a.test" {
		t.Fatalf("sites tool: %d %v", code, sites)
	}

	// notifications get 202 and no body
	code, _, _ = do(t, s, "POST", "/mcp",
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if code != http.StatusAccepted {
		t.Fatalf("notification: want 202, got %d", code)
	}

	// scan tool → enqueues on the serve runner (no worker in test, so
	// the job stays queued; cancel the request ctx once it lands to
	// end the 300s wait) then reports the job for polling.
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"scan","arguments":{"url":"http://scan.test:9","only":"http"}}}`))
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { s.ServeHTTP(rec, req); close(done) }()
	var sawJob bool
	for i := 0; i < 200; i++ {
		jobs, _ := st.RecentJobs(context.Background(), 5)
		if len(jobs) > 0 {
			sawJob = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if !sawJob {
		t.Fatal("scan tool never enqueued a job")
	}
	if rec.Code != 200 {
		t.Fatalf("scan tool: %d %s", rec.Code, rec.Body.String())
	}
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(mcpText(t, resp)), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["site"] != "scan.test:9" || payload["job"] == nil ||
		payload["state"] != "queued" {
		t.Fatalf("scan payload: %v", payload)
	}

	// the enqueued job is visible to the dashboard REST API too
	code, _, arr := do(t, s, "GET", "/api/jobs", "")
	if code != 200 || len(arr) != 1 || arr[0]["kind"] != "scan" {
		t.Fatalf("jobs api: %d %v", code, arr)
	}
}

func TestPageScan(t *testing.T) {
	s, st := seed(t)
	// scan-now {page} → job carries the page path
	code, obj, _ := do(t, s, "POST", "/api/scan-now",
		`{"key":"a.test","page":"maps/Nordsjaelland"}`)
	if code != 200 || obj["job"] == nil {
		t.Fatalf("scan-now page: %d %v", code, obj)
	}
	jobID := int64(obj["job"].(float64))
	pg, err := st.JobPage(context.Background(), jobID)
	if err != nil || pg != "maps/Nordsjaelland" {
		t.Fatalf("job page: %q %v", pg, err)
	}
	// pages endpoint parses the site's sitemap into paths
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<urlset><loc>http://x.dk/</loc>` +
			`<loc>http://x.dk/maps/Nordsjaelland/</loc>` +
			`<loc>http://x.dk/maps/Gribskov.html</loc></urlset>`))
	}))
	defer ts.Close()
	if _, err := st.GetOrCreateSite(context.Background(), "sm.test", ts.URL, 0, 0); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/api/sites/sm.test/pages", nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	var paths []string
	if err := json.Unmarshal(rec.Body.Bytes(), &paths); err != nil {
		t.Fatalf("pages: %v", err)
	}
	if len(paths) != 2 || paths[0] != "maps/Nordsjaelland/" || paths[1] != "maps/Gribskov.html" {
		t.Fatalf("pages: %v", paths)
	}
}

func TestSSEAndGzip(t *testing.T) {
	s, _ := seed(t)
	// SSE — Accept: text/event-stream must not be gzip-buffered
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest("GET", "/api/jobs/stream", nil).WithContext(ctx)
	req.Header.Set("Accept", "text/event-stream")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { s.ServeHTTP(rec, req); close(done) }()
	time.Sleep(150 * time.Millisecond) // let the first data: tick land
	cancel()
	<-done
	if !strings.Contains(rec.Body.String(), "data:") {
		t.Fatal("no SSE data")
	}
	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("SSE must not be compressed, got %s", enc)
	}
	// gzip on normal responses
	req2 := httptest.NewRequest("GET", "/api/sites", nil)
	req2.Header.Set("Accept-Encoding", "gzip")
	rec2 := httptest.NewRecorder()
	s.ServeHTTP(rec2, req2)
	if rec2.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("expected gzip")
	}
}

func TestReportPageTag(t *testing.T) {
	s, st := seed(t)
	site, _ := st.GetOrCreateSite(context.Background(), "p2.test", "http://p2.test", 10, 7)
	run := &store.Run{SiteID: site.ID, Engine: "cdp", Status: "ok",
		StartedAt: time.Now(), FinishedAt: time.Now(),
		DetailJSON: `{"url":"https://p2.test/maps/Nordsjaelland/"}`}
	runID, err := st.InsertRun(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", fmt.Sprintf("/report/%d", runID), nil)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "page maps/Nordsjaelland/") {
		t.Fatalf("report page tag: %d", rec.Code)
	}
}
