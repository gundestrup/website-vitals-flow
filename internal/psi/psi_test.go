package psi

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fakePSI points endpoint at an httptest server for the test's duration.
func fakePSI(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	old := endpoint
	endpoint = srv.URL
	t.Cleanup(func() { endpoint = old; srv.Close() })
	return srv
}

func TestFetchParsesResponse(t *testing.T) {
	var gotQuery url.Values
	fakePSI(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"loadingExperience": {"metrics": {
				"LARGEST_CONTENTFUL_PAINT_MS": {"percentile": 2600},
				"CUMULATIVE_LAYOUT_SHIFT_SCORE": {"percentile": 12}
			}},
			"lighthouseResult": {
				"categories": {"performance": {"score": 0.82}},
				"audits": {
					"largest-contentful-paint": {"numericValue": 3100},
					"total-blocking-time": {"numericValue": 420},
					"cumulative-layout-shift": {}
				}
			}
		}`)
	})
	r, err := Fetch(context.Background(), "https://example.com", "KEY")
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery.Get("url") != "https://example.com" ||
		gotQuery.Get("strategy") != "mobile" || gotQuery.Get("key") != "KEY" {
		t.Fatalf("query params: %v", gotQuery)
	}
	if !r.FieldData || r.P75["LARGEST_CONTENTFUL_PAINT_MS"] != 2600 ||
		r.P75["CUMULATIVE_LAYOUT_SHIFT_SCORE"] != 12 {
		t.Fatalf("field data: %+v", r)
	}
	if r.PerfScore != 82 {
		t.Fatalf("perf score: %v", r.PerfScore)
	}
	if r.LabLCPms == nil || *r.LabLCPms != 3100 ||
		r.LabTBTms == nil || *r.LabTBTms != 420 {
		t.Fatalf("lab metrics: %+v %+v", r.LabLCPms, r.LabTBTms)
	}
	if r.LabCLS != nil {
		t.Fatalf("missing numericValue → nil, got %+v", r.LabCLS)
	}
}

func TestFetchNoKeyNoFieldData(t *testing.T) {
	fakePSI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "" {
			t.Error("key param must be absent when apiKey empty")
		}
		_, _ = fmt.Fprint(w, `{"lighthouseResult":{"categories":{"performance":{"score":0.95}}}}`)
	})
	r, err := Fetch(context.Background(), "https://example.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if r.FieldData || len(r.P75) != 0 || r.PerfScore != 95 {
		t.Fatalf("no field data expected: %+v", r)
	}
}

func TestFetchErrorStatus(t *testing.T) {
	fakePSI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(strings.Repeat("quota exceeded ", 30)))
	})
	_, err := Fetch(context.Background(), "https://example.com", "")
	if err == nil || !strings.Contains(err.Error(), "429") {
		t.Fatalf("want status error, got %v", err)
	}
	if len(err.Error()) > 220 {
		t.Fatalf("error body should be truncated: %d chars", len(err.Error()))
	}
}

func TestFetchBadJSON(t *testing.T) {
	fakePSI(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `not json`)
	})
	if _, err := Fetch(context.Background(), "https://example.com", ""); err == nil {
		t.Fatal("want parse error")
	}
}
