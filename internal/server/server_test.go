package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gundestrup/website-vitals-flow/internal/config"
	"github.com/gundestrup/website-vitals-flow/internal/jobs"
	"github.com/gundestrup/website-vitals-flow/internal/measure"
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
		t.Fatalf("%v — full response: %v", err, resp)
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

// noFlush is a ResponseWriter that is NOT an http.Flusher.
type noFlush struct{ http.ResponseWriter }

func TestEncWriterFlush(_ *testing.T) {
	e := encWriter{ResponseWriter: httptest.NewRecorder()}
	e.Flush() // flusher branch
	e2 := encWriter{ResponseWriter: noFlush{httptest.NewRecorder()}}
	e2.Flush() // non-flusher branch — must not panic
}

func TestErrString(t *testing.T) {
	if errBadID.Error() != "bad id" || errString("x").Error() != "x" {
		t.Fatal("errString")
	}
}

func TestScanEndpoint(t *testing.T) {
	s, _ := seed(t)
	// wait:0 — enqueue only
	code, obj, _ := do(t, s, "POST", "/api/scan", `{"url":"http://scan.test:9"}`)
	if code != 200 || obj["job"] == nil || obj["site"] != "scan.test:9" {
		t.Fatalf("scan enqueue: %d %v", code, obj)
	}
	// wait>0 — waitJob/pollJob loop: job fails (unreachable host) or
	// hits the 1s wait cap; either way resp carries a state
	code, obj, _ = do(t, s, "POST", "/api/scan", `{"url":"http://scan2.test:9","wait":1}`)
	if code != 200 || obj["job"] == nil || obj["state"] == nil {
		t.Fatalf("scan wait: %d %v", code, obj)
	}
	// validation branches
	code, obj, _ = do(t, s, "POST", "/api/scan", `{"notjson"`)
	if code != 200 || obj["error"] == nil {
		t.Fatalf("bad body should report error: %d %v", code, obj)
	}
	code, obj, _ = do(t, s, "POST", "/api/scan", `{"url":""}`)
	if code != 200 || obj["error"] == nil {
		t.Fatalf("empty url should report error: %d %v", code, obj)
	}
	code, obj, _ = do(t, s, "POST", "/api/scan", `{"url":"://bad"}`)
	if code != 200 && code != 500 {
		t.Fatalf("unparseable url: %d %v", code, obj)
	}
}

func TestJobEndpoint(t *testing.T) {
	s, st := seed(t)
	ctx := context.Background()
	site, _ := st.GetOrCreateSite(ctx, "jb.test", "http://jb.test", 10, 7)
	id, err := st.InsertJob(ctx, "scan", site.ID, false, "")
	if err != nil {
		t.Fatal(err)
	}
	code, obj, _ := do(t, s, "GET", fmt.Sprintf("/api/jobs/%d", id), "")
	if code != 200 || obj["job"] == nil {
		t.Fatalf("job by id: %d %v", code, obj)
	}
	code, _, _ = do(t, s, "GET", "/api/jobs/abc", "")
	if code != 500 {
		t.Fatalf("bad id: %d", code)
	}
	code, _, _ = do(t, s, "GET", "/api/jobs/99999", "")
	if code != 500 {
		t.Fatalf("missing job: %d", code)
	}
}

func TestScanAll(t *testing.T) {
	s, _ := seed(t)
	code, obj, _ := do(t, s, "POST", "/api/scan-all", `{"deep":false}`)
	if code != 200 || obj["enqueued"].(float64) != 0 {
		t.Fatalf("no-schedule scan-all: %d %v", code, obj)
	}
	code, _, _ = do(t, s, "PATCH", "/api/sites/a.test", `{"schedule":"15m"}`)
	if code != 200 {
		t.Fatalf("patch schedule: %d", code)
	}
	code, obj, _ = do(t, s, "POST", "/api/scan-all", `{"deep":false}`)
	if code != 200 || obj["enqueued"].(float64) != 1 {
		t.Fatalf("scan-all: %d %v", code, obj)
	}
}

func TestBadgeHandler(t *testing.T) {
	s, _ := seed(t)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}
	rec := get("/badge.svg?run=1&metric=ttfb")
	if rec.Header().Get("Content-Type") != "image/svg+xml" ||
		!strings.Contains(rec.Body.String(), "42ms") {
		t.Fatalf("run badge: %s", rec.Body.String()[:80])
	}
	rec = get("/badge.svg?metric=lcp&value=2500")
	if !strings.Contains(rec.Body.String(), "2500ms") {
		t.Fatalf("value badge: %s", rec.Body.String()[:80])
	}
	rec = get("/badge.svg?metric=cls&value=0.15")
	if !strings.Contains(rec.Body.String(), "0.150") {
		t.Fatalf("cls badge: %s", rec.Body.String()[:80])
	}
	rec = get("/badge.svg?run=999&metric=ttfb")
	if !strings.Contains(rec.Body.String(), "n/a") {
		t.Fatalf("missing run badge: %s", rec.Body.String()[:80])
	}
	rec = get("/badge.svg?metric=bogus&value=abc")
	if !strings.Contains(rec.Body.String(), "n/a") {
		t.Fatalf("bad value badge: %s", rec.Body.String()[:80])
	}
}

func TestRunMetric(t *testing.T) {
	lcp, fcp := 100.0, 50.0
	r := &store.Run{TTFBms: 10, FCPms: &fcp, LCPms: &lcp, CLS: 0.1, TBTms: 20}
	for _, m := range []string{"ttfb", "fcp", "lcp", "cls", "tbt"} {
		if _, ok := runMetric(r, m); !ok {
			t.Errorf("runMetric %s", m)
		}
	}
	if _, ok := runMetric(&store.Run{}, "lcp"); ok {
		t.Error("nil lcp should miss")
	}
	if _, ok := runMetric(r, "inp"); ok {
		t.Error("unknown metric should miss")
	}
}

func TestAPIErrorPaths(t *testing.T) {
	s, _ := seed(t)
	if code, _, _ := do(t, s, "GET", "/api/runs/abc", ""); code != 500 {
		t.Fatalf("bad run id: %d", code)
	}
	if code, _, _ := do(t, s, "GET", "/api/runs/999", ""); code != 500 {
		t.Fatalf("missing run: %d", code)
	}
	if code, _, _ := do(t, s, "GET", "/api/sites/nope.test/runs", ""); code != 500 {
		t.Fatalf("unknown site runs: %d", code)
	}
	code, obj, _ := do(t, s, "POST", "/api/sites", `{"nope"`)
	if code != 200 || obj["error"] == nil {
		t.Fatalf("siteAdd bad body: %d %v", code, obj)
	}
	code, obj, _ = do(t, s, "POST", "/api/sites", `{"url":"://bad"}`)
	if code != 200 || obj["error"] == nil {
		t.Fatalf("siteAdd bad url: %d %v", code, obj)
	}
	code, obj, _ = do(t, s, "PATCH", "/api/sites/nope.test", `{"deep":true}`)
	if code != 200 || obj["error"] == nil {
		t.Fatalf("siteEdit unknown: %d %v", code, obj)
	}
	code, obj, _ = do(t, s, "PATCH", "/api/sites/a.test", `{"broken`)
	if code != 200 || obj["error"] == nil {
		t.Fatalf("siteEdit bad body: %d %v", code, obj)
	}
	code, obj, _ = do(t, s, "POST", "/api/prune", `{}`)
	if code != 200 || obj["dropped"] == nil {
		t.Fatalf("prune: %d %v", code, obj)
	}
	code, obj, _ = do(t, s, "POST", "/api/prune", `{"key":"a.test"}`)
	if code != 200 || obj["dropped"] == nil {
		t.Fatalf("prune keyed: %d %v", code, obj)
	}
}

func TestMCPNotificationAndBadBody(t *testing.T) {
	s, _ := seed(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("POST", "/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)))
	if rec.Code != 202 {
		t.Fatalf("notification: %d", rec.Code)
	}
	code, obj, _ := do(t, s, "POST", "/mcp", `{not json`)
	if code != 200 || obj["error"] == nil {
		t.Fatalf("bad mcp body: %d %v", code, obj)
	}
}

func TestJobsStreamSSE(t *testing.T) {
	s, _ := seed(t)
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+"/api/jobs/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("sse content type: %s", resp.Header.Get("Content-Type"))
	}
	buf := make([]byte, 4096)
	n, err := resp.Body.Read(buf) // first data: frame
	cancel()
	if err != nil || !strings.Contains(string(buf[:n]), "data:") {
		t.Fatalf("sse frame: %v %q", err, buf[:80])
	}
}

// A client that leaves mid-poll must not turn the tool result into a
// "context canceled" error — the job's state stands on its own.
func TestAwaitJobClientGone(t *testing.T) {
	_, st := seed(t)
	sv := &Server{st: st} // poll/await only need the store
	ctx := context.Background()
	site, _ := st.GetOrCreateSite(ctx, "gone.test", "http://gone.test", 10, 7)
	id, err := st.InsertJob(ctx, "scan", site.ID, false, "")
	if err != nil {
		t.Fatal(err)
	}
	dead, cancel := context.WithCancel(ctx)
	cancel()
	j, _, err := sv.awaitJob(dead, id)
	if err != nil {
		t.Fatalf("cancelled client should still get job state: %v", err)
	}
	if j == nil || j.State == "" {
		t.Fatalf("job: %v", j)
	}
}

func TestPollJobClientGone(t *testing.T) {
	_, st := seed(t)
	sv := &Server{st: st} // poll/await only need the store
	ctx := context.Background()
	site, _ := st.GetOrCreateSite(ctx, "gone2.test", "http://gone2.test", 10, 7)
	id, err := st.InsertJob(ctx, "scan", site.ID, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.JobState(ctx, id, "failed", "boom"); err != nil {
		t.Fatal(err)
	}
	dead, cancel := context.WithCancel(ctx)
	cancel()
	req := httptest.NewRequest("GET", "/", nil).WithContext(dead)
	rec := httptest.NewRecorder()
	resp := map[string]any{"job": id}
	done := sv.pollJob(rec, req, id, resp)
	if !done || !strings.Contains(rec.Body.String(), `"state":"failed"`) {
		t.Fatalf("pollJob gone client: done=%v body=%s", done, rec.Body.String())
	}
}

func TestIndexAndSitePages(t *testing.T) {
	s, _ := seed(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "website-vitals-flow") {
		t.Fatalf("index: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/site/a.test", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "a.test") {
		t.Fatalf("site page: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/site/nope.test", nil))
	if rec.Code != 404 {
		t.Fatalf("unknown site page: %d", rec.Code)
	}
}

func TestPageHelpers(t *testing.T) {
	if truncate("héllo wörld", 3) != "hél" {
		t.Error("truncate must be rune-safe")
	}
	if truncate("abc", 9) != "abc" {
		t.Error("truncate short")
	}
	if got := ago(time.Now().Add(-30 * time.Minute)); got != "30m ago" {
		t.Errorf("ago: %s", got)
	}
	if got := ago(time.Now().Add(-5 * time.Hour)); got != "5h ago" {
		t.Errorf("ago h: %s", got)
	}
	if got := ago(time.Now().Add(-72 * time.Hour)); got != "3d ago" {
		t.Errorf("ago d: %s", got)
	}
	if got := ageShort(time.Now().Add(-30 * time.Minute)); got != "30m" {
		t.Errorf("ageShort m: %s", got)
	}
	if got := ageShort(time.Now().Add(-5 * time.Hour)); got != "5h" {
		t.Errorf("ageShort h: %s", got)
	}
	if got := ageShort(time.Now().Add(-72 * time.Hour)); got != "3d" {
		t.Errorf("ageShort d: %s", got)
	}
	if got := ageShort(time.Now().Add(-800 * 24 * time.Hour)); got != "2y" {
		t.Errorf("ageShort y: %s", got)
	}
	if iconFor(true) == iconFor(false) {
		t.Error("iconFor should differ")
	}
	var nilf *float64
	if fmtOpt(nilf) != "-" {
		t.Error("fmtOpt nil")
	}
	v := 42.4
	if fmtOpt(&v) != "42" {
		t.Error("fmtOpt")
	}
	if lastSample(&measure.Result{}) != nil || lastSample(nil) != nil {
		t.Error("lastSample empty")
	}
	if parseDetail(`{bad`) != nil {
		t.Error("parseDetail bad json")
	}
	if !strings.Contains(prettyJSON(`{"a":1}`), "\n") {
		t.Error("prettyJSON should indent")
	}
}

func TestViewModelHelpers(t *testing.T) {
	d := parseDetail(`{"samples":[
		{"ttfb_ms":10,"console":[{"kind":"warn","text":"w"}],"screenshot":"AAA"},
		{"ttfb_ms":20,"console":[{"kind":"error","text":"e"}],"screenshot":"BBB"}],
		"findings":[
			{"id":"f1","title":"t","detail":"d","severity":"poor"},
			{"id":"f1","title":"t","detail":"d","severity":"poor"},
			{"id":"f2","title":"t2","detail":"d2","severity":"ok"}]}`)
	if d == nil {
		t.Fatal("parseDetail")
	}
	if smp := lastSample(d); smp == nil || smp.TTFBms != 20 {
		t.Fatal("lastSample picks last")
	}
	if shotB64(d) != "BBB" {
		t.Error("shotB64")
	}
	if v := findingVMs(d); len(v) != 2 {
		t.Fatalf("findingVMs dedup: %v", v)
	}
	cv := consoleVMs(d)
	if len(cv) != 1 || cv[0].Kind != "error" || cv[0].Class != "sev-poor" {
		t.Fatalf("consoleVMs: %v", cv)
	}
	if bandOf("lcp", nil) == "" {
		t.Error("bandOf nil")
	}
}

func TestCardBuilders(t *testing.T) {
	d := parseDetail(`{"url":"http://a.test","samples":[{
			"ttfb_ms":40,"fcp_ms":100,"lcp_ms":200,"cls":0.01,"tbt_ms":50,
			"js_heap_bytes":1024,"dom_nodes":500,
			"responses":[{"url":"http://a.test/","encoded_bytes":1000,"protocol":"h2","status":200}],
			"console":[{"kind":"warn","text":"w1"},{"kind":"error","text":"e1"}],
			"screenshot":"AAA"}],
		"http_checks":{
			"proto":"HTTP/2.0","tls_version":"TLS 1.3","alt_svc":"h3",
			"encodings":{"br":{"ok":true,"bytes":500},"gzip":{"ok":false,"error":"not offered"}},
			"cache":[{"url":"/a.js","cacheable":true},{"url":"/b.js","cacheable":false,"reasons":["no-store"]}],
			"signals":{"expires":true}},
		"findings":[{"id":"f1","title":"t","detail":"d","severity":"warn"}]}`)
	if d == nil {
		t.Fatal("parseDetail")
	}
	r := &store.Run{TTFBms: 40}
	cards := buildCards(r, d, "3d")
	if len(cards) != 11 { // 5 vitals + pagesize/proto/comp/tls/cache/console
		t.Fatalf("cards: %d", len(cards))
	}
	var console, cache metricCard
	for _, c := range cards {
		switch c.Label {
		case "console":
			console = c
		case "cacheable":
			cache = c
		}
	}
	if console.Band != "poor" || len(console.Lines) != 2 {
		t.Errorf("console card: %+v", console)
	}
	if cache.Label == "" || cache.Text == "" {
		t.Error("cacheable card missing")
	}
}

func TestRunRowsFormat(t *testing.T) {
	lcp := 2500.0
	rows := runRows("a.test", []store.Run{{
		ID: 3, Engine: "cdp", StartedAt: time.Now(),
		TTFBms: 42, LCPms: &lcp, CLS: 0.123, TBTms: 55, HTTPProtocol: "h2",
	}})
	if len(rows) != 1 || rows[0].TTFB != "42" || rows[0].LCP != "2500" ||
		rows[0].CLS != "0.123" || rows[0].Proto != "h2" {
		t.Fatalf("runRows: %+v", rows[0])
	}
}

// Static + meta endpoints — exercises the New() route table and the
// embedded static handler; a missing asset goes through FileServer's
// 404 path.
func TestStaticEndpoints(t *testing.T) {
	s, _ := seed(t)
	for _, p := range []string{
		"/robots.txt", "/sitemap.xml", "/site.webmanifest",
		"/favicon.svg", "/favicon.ico", "/favicon-96x96.png",
		"/apple-touch-icon.png", "/web-app-manifest-192x192.png",
		"/web-app-manifest-512x512.png", "/app.js", "/theme.js",
		"/style.css",
	} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != 200 {
			t.Errorf("GET %s: want 200, got %d", p, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/no-such-asset.txt", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing asset: want 404, got %d", rec.Code)
	}
}

// WVF_DEV_STATIC serves files straight from a directory (dev loop);
// a nonexistent override dir falls back to the embedded assets. The
// override also sources page templates, so the dir needs a full copy.
func TestStaticDevOverride(t *testing.T) {
	dir := t.TempDir()
	ents, err := os.ReadDir("static")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if e.IsDir() {
			continue // vendor/ — not needed for templates or this probe
		}
		data, err := os.ReadFile("static/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dir+"/"+e.Name(), data, 0o600); err != nil { // #nosec G703 -- names from own static dir
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(dir+"/probe.js", []byte("// dev override\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WVF_DEV_STATIC", dir)
	s, _ := seed(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest("GET", "/probe.js", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "dev override") {
		t.Fatalf("dev static: %d %q", rec.Code, rec.Body.String())
	}

	t.Setenv("WVF_DEV_STATIC", dir+"/missing")
	s2, _ := seed(t)
	rec = httptest.NewRecorder()
	s2.ServeHTTP(rec, httptest.NewRequest("GET", "/app.js", nil))
	if rec.Code != 200 {
		t.Fatalf("embedded fallback after bad override: %d", rec.Code)
	}
}

// scanViaRunner rejects unparseable URLs before enqueueing.
func TestMCPScanBadURL(t *testing.T) {
	s, _ := seed(t)
	code, obj, _ := do(t, s, "POST", "/mcp",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"scan","arguments":{"url":"http://%"}}}`)
	if code != 200 {
		t.Fatalf("code %d", code)
	}
	res := obj["result"].(map[string]any)
	if res["isError"] != true {
		t.Fatalf("want isError result for bad url, got %v", res)
	}
}

// A bogus browser makes the queued scan job fail deterministically —
// scanViaRunner must surface "scan failed: <detail>".
func TestMCPScanFailedJob(t *testing.T) {
	st, err := store.Open(context.Background(), t.TempDir()+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := config.Default()
	cfg.BrowserPath = "/nonexistent/wvf-test-browser"
	r := jobs.New(st, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go r.Start(ctx)
	h := New(st, cfg, r)

	code, obj, _ := do(t, h, "POST", "/mcp",
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"scan","arguments":{"url":"http://127.0.0.1:9/"}}}`)
	if code != 200 {
		t.Fatalf("code %d", code)
	}
	if txt := mcpText(t, obj); !strings.Contains(txt, "scan failed") {
		t.Fatalf("want scan-failed report, got %q", txt)
	}
}

// Bad run/value params hit fmtSscanf's and fmtSscanfloat's errBadID
// branches — the badge must degrade to the n/a fallback, not 500.
func TestBadgeBadParams(t *testing.T) {
	s, _ := seed(t)
	for _, p := range []string{
		"/badge.svg?metric=lcp&run=abc",     // non-digit run id
		"/badge.svg?metric=cls&value=1.2.3", // second dot
		"/badge.svg?metric=lcp&value=abc",   // non-digit value
	} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if rec.Code != 200 {
			t.Errorf("GET %s: want 200, got %d", p, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "svg") {
			t.Errorf("GET %s: want svg body", p)
		}
	}
}

// Both JobByID reads fail (cancelled ctx + dead store) — pollJob must
// still write the accumulated resp instead of an error.
func TestPollJobFallbackOnDeadStore(t *testing.T) {
	st, err := store.Open(context.Background(), t.TempDir()+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{st: st}
	_ = st.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	resp := map[string]any{"state": "queued"}
	if !srv.pollJob(rec, req, 1, resp) {
		t.Fatal("pollJob should write the fallback resp")
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["state"] != "queued" {
		t.Fatalf("want accumulated state, got %s", rec.Body.String())
	}
}

// Client leaves after one successful poll, then the store dies —
// awaitJob returns the last-seen job rather than an error.
func TestAwaitJobFallbackLastSeen(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir()+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	site, err := st.GetOrCreateSite(ctx, "f.test", "http://f.test", 10, 7)
	if err != nil {
		t.Fatal(err)
	}
	jobID, err := st.InsertJob(ctx, "scan", site.ID, false, "")
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{st: st}

	reqCtx, cancel := context.WithCancel(ctx)
	type result struct {
		j     *store.Job
		runID int64
		err   error
	}
	done := make(chan result, 1)
	go func() {
		j, rid, err := srv.awaitJob(reqCtx, jobID)
		done <- result{j, rid, err}
	}()
	// first poll (500ms tick) must complete before we kill the store
	time.Sleep(800 * time.Millisecond)
	_ = st.Close()
	cancel()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("awaitJob: %v", r.err)
		}
		if r.j == nil || r.j.State != "queued" {
			t.Fatalf("want last-seen queued job, got %+v", r.j)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("awaitJob did not return after cancel")
	}
}
