package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/config"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/jobs"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/store"
)

func seed(t *testing.T) http.Handler {
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
	return New(st, cfg, jobs.New(st, cfg))
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
	s := seed(t)
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
	s := seed(t)
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
	s := seed(t)
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
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Monitored sites") {
		t.Fatalf("site route: %d", rec.Code)
	}
	req = httptest.NewRequest("GET", "/report/1", nil)
	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "cls 0.3") {
		t.Fatalf("report: %d %s", rec.Code, rec.Body.String()[:200])
	}
}
