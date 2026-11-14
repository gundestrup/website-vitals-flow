package httpcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// testServer echoes Accept-Encoding back as Content-Encoding (as a
// negotiating origin would), sets doc-level cache headers, and answers
// the conventional basics paths with distinct statuses.
func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/old", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/robots.txt", func(_ http.ResponseWriter, _ *http.Request) {})
	mux.HandleFunc("/favicon.ico", func(_ http.ResponseWriter, _ *http.Request) {})
	mux.HandleFunc("/sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/site.webmanifest", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "wvf-test")
		w.Header().Set("Cache-Control", "public, max-age=60")
		w.Header().Set("ETag", `"v1"`)
		if ae := r.Header.Get("Accept-Encoding"); ae != "" && ae != "identity" {
			w.Header().Set("Content-Encoding", ae)
		}
		_, _ = w.Write([]byte("doc"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestProbe(t *testing.T) {
	srv := testServer(t)
	res, err := Probe(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 || res.FinalURL != srv.URL {
		t.Fatalf("status/final: %d %q", res.Status, res.FinalURL)
	}
	if res.Proto != "HTTP/1.1" || res.Server != "wvf-test" {
		t.Fatalf("proto/server: %q %q", res.Proto, res.Server)
	}
	if res.TLSVersion != "" {
		t.Fatalf("plain http should report no TLS, got %q", res.TLSVersion)
	}
	if !res.Signals["cache-control"] || !res.Signals["etag"] {
		t.Fatalf("want cache-control+etag signals: %v", res.Signals)
	}
	if res.Signals["x-cache"] || res.Signals["age"] {
		t.Fatalf("unset headers must not signal: %v", res.Signals)
	}
	for _, enc := range []string{"br", "zstd", "gzip"} {
		p := res.Encodings[enc]
		if p == nil || !p.OK || p.Returned != enc {
			t.Fatalf("encoding %s: %+v", enc, p)
		}
	}
	id := res.Encodings["identity"]
	if id == nil || !id.OK || id.Returned != "" {
		t.Fatalf("identity probe: %+v", id)
	}
	if id.Bytes != 3 {
		t.Fatalf("identity bytes: %d, want 3", id.Bytes)
	}
	want := map[string]int{
		"/robots.txt": 200, "/favicon.ico": 200,
		"/sitemap.xml": 404, "/site.webmanifest": 404,
	}
	for p, st := range want {
		if res.Basics[p] != st {
			t.Fatalf("basics %s: %d, want %d", p, res.Basics[p], st)
		}
	}
}

func TestProbeFollowsRedirects(t *testing.T) {
	srv := testServer(t)
	res, err := Probe(context.Background(), srv.URL+"/old")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 || res.FinalURL != srv.URL+"/" {
		t.Fatalf("redirect result: %d %q", res.Status, res.FinalURL)
	}
	if len(res.Redirects) != 1 || res.Redirects[0] != srv.URL+"/" {
		t.Fatalf("redirects: %v", res.Redirects)
	}
}

func TestProbeUnreachable(t *testing.T) {
	srv := testServer(t)
	srv.Close() // listener gone → fetch must error, not panic
	if _, err := Probe(context.Background(), srv.URL); err == nil {
		t.Fatal("want error for unreachable target")
	}
}

func TestCacheVerdicts(t *testing.T) {
	out := CacheVerdicts([]Asset{
		{URL: "http://x/a.css", Status: 200,
			Headers: map[string]string{"Cache-Control": "public, max-age=3600"}},
		{URL: "http://x/b.css", Status: 200,
			Headers: map[string]string{"Cache-Control": "no-store"}},
		{URL: "://bad-url", Status: 200, Headers: nil}, // skipped
	})
	if len(out) != 2 {
		t.Fatalf("want 2 verdicts, got %d", len(out))
	}
	if !out[0].Cacheable || out[0].Expires == "" {
		t.Fatalf("max-age asset: %+v", out[0])
	}
	if out[1].Cacheable || len(out[1].Reasons) == 0 {
		t.Fatalf("no-store asset must list reasons: %+v", out[1])
	}
	if !strings.Contains(out[1].Reasons[0], "NoStore") {
		t.Fatalf("want a store-related reason, got %v", out[1].Reasons)
	}
}
