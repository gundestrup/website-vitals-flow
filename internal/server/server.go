// Package server hosts the wvf dashboard: JSON API + embedded static UI.
package server

import (
	"compress/gzip"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"

	"time"

	"github.com/andybalholm/brotli"
	"github.com/gundestrup/website-vitals-flow/internal/config"
	"github.com/gundestrup/website-vitals-flow/internal/jobs"
	"github.com/gundestrup/website-vitals-flow/internal/mcpserver"
	"github.com/gundestrup/website-vitals-flow/internal/measure"
	"github.com/gundestrup/website-vitals-flow/internal/score"
	"github.com/gundestrup/website-vitals-flow/internal/sitekey"
	"github.com/gundestrup/website-vitals-flow/internal/store"
)

//go:embed static
var staticFS embed.FS

const (
	hdrContentType = "Content-Type"
	ctHTML         = "text/html; charset=utf-8"
	errUnknownSite = "unknown site"
)

// Server bundles deps for handlers.
type Server struct {
	st     *store.Store
	cfg    config.Config
	runner *jobs.Runner
	mcpd   *mcpserver.Dispatcher
	tmpl   map[string]*template.Template // parsed once: layout+partials+page
}

// New builds the handler. WVF_DEV_STATIC=<dir> serves assets from disk.
// gzip+br compresses responses when the client asks — the dashboard's
// own scan flagged "brotli not negotiated" on its static assets.
type encWriter struct {
	http.ResponseWriter
	w io.Writer
}

func (e encWriter) Write(b []byte) (int, error) { return e.w.Write(b) }

// Flush keeps http.Flusher through the wrap so SSE handlers don't 503.
func (e encWriter) Flush() {
	if f, ok := e.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func gzipMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ae := r.Header.Get("Accept-Encoding")
		// never compress SSE — gzip/br buffer breaks real-time flush
		if r.Header.Get("Accept") == "text/event-stream" {
			h.ServeHTTP(w, r)
			return
		}
		var wtr io.WriteCloser
		switch {
		case strings.Contains(ae, "br"):
			w.Header().Set("Content-Encoding", "br")
			wtr = brotli.NewWriter(w)
		case strings.Contains(ae, "gzip"):
			w.Header().Set("Content-Encoding", "gzip")
			wtr = gzip.NewWriter(w)
		default:
			h.ServeHTTP(w, r)
			return
		}
		defer func() { _ = wtr.Close() }()
		h.ServeHTTP(encWriter{w, wtr}, r)
	})
}

// New builds the dashboard+API handler with gzip + headers middleware.
func New(st *store.Store, cfg config.Config, r *jobs.Runner) http.Handler {
	s := &Server{st: st, cfg: cfg, runner: r, tmpl: parseTemplates()}
	// /mcp shares the mcpserver dispatch (same tools as `wvf mcp` stdio)
	// but the scan tool goes through the runner — see scanViaRunner.
	s.mcpd = mcpserver.New(st, s.scanViaRunner)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sites", s.sites)
	mux.HandleFunc("GET /api/sites/{key}/runs", s.runs)
	mux.HandleFunc("GET /api/runs/{id}", s.run)
	mux.HandleFunc("GET /api/jobs", s.jobs)
	mux.HandleFunc("GET /api/jobs/stream", s.jobsStream)
	mux.HandleFunc("GET /api/jobs/{id}", s.job)
	mux.HandleFunc("GET /badge.svg", s.badge)
	// front page is a template now; /site/<key> renders its own
	// server-side detail view (':' is legal in a path segment, so
	// /site/localhost:8473 works directly)
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /site/{key}", s.sitePage)
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, "text/plain")
		_, _ = w.Write([]byte("User-agent: *\nAllow: /\n"))
	})
	mux.HandleFunc("GET /sitemap.xml", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>/</loc></url></urlset>`))
	})
	mux.HandleFunc("GET /site.webmanifest", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, "application/manifest+json")
		http.ServeFileFS(w, r, staticFS, "static/site.webmanifest")
	})
	mux.HandleFunc("GET /favicon.svg", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, staticFS, "static/favicon.svg")
	})
	mux.HandleFunc("GET /favicon-96x96.png", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, staticFS, "static/favicon-96x96.png")
	})
	mux.HandleFunc("GET /apple-touch-icon.png", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, staticFS, "static/apple-touch-icon.png")
	})
	for _, f := range []string{"web-app-manifest-192x192.png", "web-app-manifest-512x512.png"} {
		f := f
		mux.HandleFunc("GET /"+f, func(w http.ResponseWriter, r *http.Request) {
			http.ServeFileFS(w, r, staticFS, "static/"+f)
		})
	}
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, "image/svg+xml")
		http.ServeFileFS(w, r, staticFS, "static/favicon.ico")
	})
	mux.HandleFunc("GET /site/{key}/report/{id}", s.report)
	mux.HandleFunc("POST /api/scan", s.scan)
	mux.HandleFunc("POST /api/scan-now", s.scanNow)
	mux.HandleFunc("POST /api/scan-all", s.scanAll)
	mux.HandleFunc("GET /api/sites/{key}/pages", s.pages)
	mux.HandleFunc("POST /api/sites", s.siteAdd)
	mux.HandleFunc("PATCH /api/sites/{key}", s.siteEdit)
	mux.HandleFunc("DELETE /api/sites/{key}", s.siteDelete)
	mux.HandleFunc("POST /api/prune", s.prune)
	mux.HandleFunc("POST /mcp", s.mcp)
	mux.HandleFunc("GET /report/{id}", s.report)
	mux.Handle("GET /", s.static())
	return headers(gzipMiddleware(mux))
}

func (s *Server) static() http.Handler {
	var fsys http.FileSystem
	if dir := os.Getenv("WVF_DEV_STATIC"); dir != "" {
		// #nosec G703 -- WVF_DEV_STATIC is an explicit local dev override.
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			fsys = http.FS(os.DirFS(dir))
		}
	}
	if fsys == nil {
		sub, _ := fs.Sub(staticFS, "static")
		fsys = http.FS(sub)
	}
	return http.FileServer(fsys)
}

func (s *Server) sites(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.ListSites(r.Context())
	writeJSON(w, list, err)
}

func (s *Server) runs(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	site, err := s.st.SiteByKey(r.Context(), key)
	if err != nil {
		writeJSON(w, map[string]string{"error": errUnknownSite}, err)
		return
	}
	runs, err := s.st.RunsForSite(r.Context(), site.ID, 100)
	writeJSON(w, runs, err)
}

func (s *Server) run(w http.ResponseWriter, r *http.Request) {
	var id int64
	if _, err := fmtSscanf(r.PathValue("id"), &id); err != nil {
		writeJSON(w, nil, err)
		return
	}
	run, err := s.st.RunByID(r.Context(), id)
	writeJSON(w, run, err)
}

func (s *Server) jobs(w http.ResponseWriter, r *http.Request) {
	list, err := s.st.RecentJobs(r.Context(), 20)
	writeJSON(w, list, err)
}

// jobsStream pushes the jobs list over SSE whenever it changes — h1.1
// async without polling (h2 needs TLS; SSE doesn't).
func (s *Server) jobsStream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "no SSE", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	var last string
	for {
		list, err := s.st.RecentJobs(r.Context(), 20)
		if err == nil {
			if b, err := json.Marshal(list); err == nil && string(b) != last {
				last = string(b)
				// nosemgrep -- SSE frame; b is json.Marshal output
				_, _ = fmt.Fprintf(w, "data: %s\n\n", b)
				fl.Flush()
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Server) scanNow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key  string `json:"key"`
		Deep bool   `json:"deep"`
		Only string `json:"only"`
		Page string `json:"page"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, nil, err)
		return
	}
	site, err := s.st.SiteByKey(r.Context(), body.Key)
	if err != nil {
		writeJSON(w, map[string]string{"error": errUnknownSite}, err)
		return
	}
	// request-level overrides fall back to the site's own settings
	only := body.Only
	if only == "" {
		only = site.Only
	}
	id, err := s.runner.Enqueue(r.Context(), site.ID, "scan", body.Deep || site.Deep, only, body.Page)
	writeJSON(w, map[string]int64{"job": id}, err)
}

// pages returns the site's sitemap-derived paths for page scanning.
func (s *Server) pages(w http.ResponseWriter, r *http.Request) {
	site, err := s.st.SiteByKey(r.Context(), r.PathValue("key"))
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	base := strings.TrimSuffix(site.URL, "/")
	// #nosec G704 -- URL is operator-stored site config; wvf is a scanner.
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, base+"/sitemap.xml", nil)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	resp, err := http.DefaultClient.Do(req) // #nosec G704 -- same: operator-stored URL
	if err != nil {
		writeJSON(w, []string{"/"}, nil)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var urls []string
	for _, m := range locRe.FindAllStringSubmatch(string(b), -1) {
		u, err := url.Parse(m[1])
		if err != nil || u.Path == "" || u.Path == "/" {
			continue
		}
		urls = append(urls, strings.TrimPrefix(u.Path, "/"))
	}
	if len(urls) == 0 {
		urls = []string{"/"}
	}
	writeJSON(w, urls, nil)
}

var locRe = regexp.MustCompile(`<loc>([^<]+)</loc>`)

// scanAll enqueues a scan for every monitored site (schedule set).
// deep=true forces deep mode; deep=false uses each site's own flag.
func (s *Server) scanAll(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Deep bool `json:"deep"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // empty body ok
	sites, err := s.st.ListSites(r.Context())
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	n := 0
	for _, site := range sites {
		if site.Schedule == "" {
			continue
		}
		if _, err := s.runner.Enqueue(r.Context(), site.ID, "scan",
			body.Deep || site.Deep, site.Only); err == nil {
			n++
		}
	}
	writeJSON(w, map[string]int{"enqueued": n}, nil)
}

func fmtSscanf(s string, id *int64) (int, error) {
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, errBadID
		}
		n = n*10 + int64(c-'0')
	}
	*id = n
	return 1, nil
}

var errBadID = errString("bad id")

// badge serves shields.io-style SVG badges: ?metric=lcp&value=532&site=k
// or ?run=<id>&metric=lcp — reads the metric from the stored run.
func (s *Server) badge(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	metric := q.Get("metric")
	var value float64
	var ok bool
	if id := q.Get("run"); id != "" {
		var rid int64
		if _, err := fmtSscanf(id, &rid); err == nil {
			if run, err := s.st.RunByID(r.Context(), rid); err == nil {
				value, ok = runMetric(run, metric)
			}
		}
	}
	if !ok {
		// raw value mode: ?value=532 or ?cls=0.2
		if v := q.Get("value"); v != "" {
			var f float64
			if _, err := fmtSscanfloat(v, &f); err == nil {
				value, ok = f, true
			}
		}
	}
	label := metric
	vtext := "n/a"
	band := score.Band("")
	if ok {
		vtext = fmtVal(metric, value)
		band = score.Metric(metric, value)
	}
	w.Header().Set(hdrContentType, "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=300")
	// #nosec G705 -- label/vtext are XML-escaped inside BadgeSVG.
	// nosemgrep -- svg response, values escaped in score.BadgeSVG
	_, _ = w.Write([]byte(score.BadgeSVG(label, vtext, band)))
}

func runMetric(r *store.Run, metric string) (float64, bool) {
	switch metric {
	case "ttfb":
		return r.TTFBms, true
	case "fcp":
		if r.FCPms != nil {
			return *r.FCPms, true
		}
	case "lcp":
		if r.LCPms != nil {
			return *r.LCPms, true
		}
	case "cls":
		return r.CLS, true
	case "tbt":
		return r.TBTms, true
	}
	return 0, false
}

func fmtVal(metric string, v float64) string {
	if metric == "cls" {
		return fmt.Sprintf("%.3f", v)
	}
	return fmt.Sprintf("%.0fms", v)
}

func fmtSscanfloat(s string, f *float64) (int, error) {
	var neg bool
	i := 0
	if len(s) > 0 && s[0] == '-' {
		neg = true
		i = 1
	}
	var intp, frac, fracdiv float64
	fracdiv = 1
	seen, inFrac := false, false
	for ; i < len(s); i++ {
		c := s[i]
		if c == '.' {
			if inFrac {
				return 0, errBadID
			}
			inFrac = true
			continue
		}
		if c < '0' || c > '9' {
			return 0, errBadID
		}
		seen = true
		if !inFrac {
			intp = intp*10 + float64(c-'0')
		} else {
			fracdiv *= 10
			frac += float64(c-'0') / fracdiv
		}
	}
	if !seen {
		return 0, errBadID
	}
	*f = intp + frac
	if neg {
		*f = -*f
	}
	return 1, nil
}

// scan is the IDE/agent entry point: POST {"url": ..., "wait": seconds}.
// Normalizes the URL to a site key (created on first sight), enqueues,
// optionally waits for the result. Response: {"job", "site", "run"?}.
func (s *Server) scan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL  string `json:"url"`
		Wait int    `json:"wait"`
		Deep bool   `json:"deep"`
		Only string `json:"only"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.URL == "" {
		writeJSON(w, map[string]string{"error": "need url field"}, nil)
		return
	}
	key, canon, err := sitekey.From(body.URL)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	site, err := s.st.GetOrCreateSite(r.Context(), key, canon,
		s.cfg.KeepLast, s.cfg.MaxAgeDays)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	jobID, err := s.runner.Enqueue(r.Context(), site.ID, "scan", site.Deep, body.Only)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	resp := map[string]any{"job": jobID, "site": site.Key}
	if body.Wait <= 0 {
		writeJSON(w, resp, nil)
		return
	}
	s.waitJob(w, r, jobID, body.Wait, resp)
}

// waitJob polls the job every 500ms until done/failed, the client
// disconnects, or wait seconds elapse (capped at 300) — then writes resp.
func (s *Server) waitJob(w http.ResponseWriter, r *http.Request, jobID int64, wait int, resp map[string]any) {
	if wait > 300 {
		wait = 300
	}
	deadline := time.Now().Add(time.Duration(wait) * time.Second)
	for time.Now().Before(deadline) {
		if s.pollJob(w, r, jobID, resp) {
			return
		}
		select {
		case <-r.Context().Done():
			writeJSON(w, resp, nil)
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
	resp["state"] = "timeout"
	writeJSON(w, resp, nil)
}

// pollJob checks the job once and updates resp; returns true when a
// response was written (store error or terminal job state).
func (s *Server) pollJob(w http.ResponseWriter, r *http.Request, jobID int64, resp map[string]any) bool {
	j, runID, err := s.st.JobByID(r.Context(), jobID)
	if err != nil && r.Context().Err() != nil {
		// client gone mid-poll — retry once on a fresh short context;
		// if that also fails, fall back to the accumulated resp state
		// rather than an error nobody will read
		fresh, stop := context.WithTimeout(context.Background(), 5*time.Second) // NOSONAR -- request ctx is dead; a detached read is the point
		j, runID, err = s.st.JobByID(fresh, jobID)
		stop()
		if err != nil {
			writeJSON(w, resp, nil)
			return true
		}
	}
	if err != nil {
		writeJSON(w, nil, err)
		return true
	}
	resp["state"] = j.State
	resp["detail"] = j.Detail
	if j.State != "done" && j.State != "failed" {
		return false
	}
	if runID > 0 {
		run, rerr := s.st.RunByID(r.Context(), runID)
		if rerr == nil {
			resp["run_id"] = runID
			resp["run"] = run
		}
	}
	writeJSON(w, resp, nil)
	return true
}

// mcpWait is the scan-tool wait cap — same 300s as /api/scan's wait.
const mcpWait = 300 * time.Second

// mcp serves MCP over HTTP: one JSON-RPC 2.0 request per POST, shared
// dispatch with `wvf mcp` stdio (initialize, tools/list, tools/call).
// Notifications get 202 — the JSON-RPC spec expects no response.
func (s *Server) mcp(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	out, ok := s.mcpd.Dispatch(r.Context(), body)
	if !ok {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set(hdrContentType, "application/json")
	// nosemgrep -- JSON-RPC response bytes from mcpserver.Dispatch
	_, _ = w.Write(out)
}

// scanViaRunner is the /mcp "scan" tool impl — the one place the shared
// dispatch can't be fully transport-agnostic: stdio runs RunSiteScan
// inline, but here the work must go through the serve Runner so scans
// stay single-slot and surface on the dashboard. Enqueues, waits like
// /api/scan's waitJob (500ms poll, ≤300s), then shapes the same result
// from the persisted run. The `samples` arg can't ride the job queue
// (jobs carry only/deep, not samples) — cfg.Samples applies.
func (s *Server) scanViaRunner(ctx context.Context, a mcpserver.ScanArgs) (map[string]any, error) {
	key, canon, err := sitekey.From(a.URL)
	if err != nil {
		return nil, err
	}
	site, err := s.st.GetOrCreateSite(ctx, key, canon,
		s.cfg.KeepLast, s.cfg.MaxAgeDays)
	if err != nil {
		return nil, err
	}
	jobID, err := s.runner.Enqueue(ctx, site.ID, "scan", a.Deep || site.Deep, a.Only)
	if err != nil {
		return nil, err
	}
	j, runID, err := s.awaitJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if j.State == "failed" {
		return nil, fmt.Errorf("scan failed: %s", j.Detail)
	}
	if j.State != "done" || runID == 0 {
		// still queued/running at the wait cap (or the client left) —
		// report the job so the agent can poll via the jobs tool
		return map[string]any{"site": site.Key, "job": jobID, "state": j.State}, nil
	}
	run, err := s.st.RunByID(ctx, runID)
	if err != nil {
		return nil, err
	}
	var res measure.Result
	if err := json.Unmarshal([]byte(run.DetailJSON), &res); err == nil &&
		len(res.Samples) > 0 {
		return mcpserver.ScanPayload(site.Key, runID, &res), nil
	}
	return map[string]any{ // degraded: headline columns only
		"site": site.Key, "run_id": runID,
		"ttfb_ms": run.TTFBms, "fcp_ms": run.FCPms, "lcp_ms": run.LCPms,
		"cls": run.CLS, "tbt_ms": run.TBTms, "requests": run.RequestCount,
	}, nil
}

// awaitJob polls every 500ms until the job reaches a terminal state,
// ctx ends, or mcpWait elapses — the waitJob pattern minus the writer.
// Returns the last-seen job, so a non-terminal state means "still
// going" rather than an error.
func (s *Server) awaitJob(ctx context.Context, jobID int64) (*store.Job, int64, error) {
	deadline := time.Now().Add(mcpWait)
	var j *store.Job
	var runID int64
	for {
		jj, rid, err := s.st.JobByID(ctx, jobID)
		if err != nil && ctx.Err() != nil {
			// client left mid-poll — the job's state didn't change
			// with it, so retry once on a fresh short context; the
			// caller still wants the queued-job report
			fresh, stop := context.WithTimeout(context.Background(), 5*time.Second) // NOSONAR -- ctx is dead; a detached read is the point
			jj, rid, err = s.st.JobByID(fresh, jobID)
			stop()
			if err == nil {
				return jj, rid, nil
			}
			if j != nil {
				return j, runID, nil
			}
		}
		if err != nil {
			return nil, 0, err
		}
		j, runID = jj, rid
		if j.State == "done" || j.State == "failed" ||
			time.Now().After(deadline) {
			return j, runID, nil
		}
		select {
		case <-ctx.Done():
			return j, runID, nil
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// job returns one job + its run id for polling clients.
func (s *Server) job(w http.ResponseWriter, r *http.Request) {
	var id int64
	if _, err := fmtSscanf(r.PathValue("id"), &id); err != nil {
		writeJSON(w, nil, err)
		return
	}
	j, runID, err := s.st.JobByID(r.Context(), id)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, map[string]any{"job": j, "run_id": runID}, nil)
}

type errString string

func (e errString) Error() string { return string(e) }

func writeJSON(w http.ResponseWriter, v any, err error) {
	w.Header().Set(hdrContentType, "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		v = map[string]string{"error": err.Error()}
	}
	_ = json.NewEncoder(w).Encode(v)
}

func headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// siteAdd creates/updates a monitored site from the dashboard.
func (s *Server) siteAdd(w http.ResponseWriter, r *http.Request) {
	var b struct {
		URL      string `json:"url"`
		Label    string `json:"label"`
		Schedule string `json:"schedule"`
		Deep     bool   `json:"deep"`
		Only     string `json:"only"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil || b.URL == "" {
		writeJSON(w, map[string]string{"error": "need url"}, nil)
		return
	}
	key, canon, err := sitekey.From(b.URL)
	if err != nil {
		writeJSON(w, map[string]string{"error": err.Error()}, nil)
		return
	}
	site, err := s.st.UpsertSite(r.Context(), store.SiteOpts{
		Key: key, URL: canon, Label: b.Label, Schedule: b.Schedule,
		KeepLast: s.cfg.KeepLast, MaxAge: s.cfg.MaxAgeDays,
		Deep: b.Deep, Only: b.Only,
	})
	writeJSON(w, site, err)
}

// siteEdit patches a monitored site's label/schedule/deep/only.
func (s *Server) siteEdit(w http.ResponseWriter, r *http.Request) {
	site, err := s.st.SiteByKey(r.Context(), r.PathValue("key"))
	if err != nil {
		writeJSON(w, map[string]string{"error": errUnknownSite}, nil)
		return
	}
	var b struct {
		Label    *string `json:"label"`
		Schedule *string `json:"schedule"`
		Deep     *bool   `json:"deep"`
		Only     *string `json:"only"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeJSON(w, map[string]string{"error": "bad body"}, nil)
		return
	}
	label, sched, deep, only := site.Label, site.Schedule, site.Deep, site.Only
	if b.Label != nil {
		label = *b.Label
	}
	if b.Schedule != nil {
		sched = *b.Schedule
	}
	if b.Deep != nil {
		deep = *b.Deep
	}
	if b.Only != nil {
		only = *b.Only
	}
	updated, err := s.st.UpsertSite(r.Context(), store.SiteOpts{
		Key: site.Key, URL: site.URL, Label: label, Schedule: sched,
		KeepLast: site.KeepLast, MaxAge: site.MaxAgeDays,
		Deep: deep, Only: only,
	})
	writeJSON(w, updated, err)
}

func (s *Server) siteDelete(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"removed": r.PathValue("key")},
		s.st.RemoveSite(r.Context(), r.PathValue("key")))
}

// prune applies retention now — optional {"key": site} for one site,
// or {"older_days": 60} to force an age cutoff across sites.
func (s *Server) prune(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Key string `json:"key"`
	}
	_ = json.NewDecoder(r.Body).Decode(&b)
	var dropped int64
	if b.Key != "" {
		if site, err := s.st.SiteByKey(r.Context(), b.Key); err == nil {
			n, _ := s.st.PruneSiteNow(r.Context(), site.ID)
			dropped = n
		}
	} else {
		sites, _ := s.st.ListSites(r.Context())
		for _, si := range sites {
			n, _ := s.st.PruneSiteNow(r.Context(), si.ID)
			dropped += n
		}
	}
	writeJSON(w, map[string]int64{"dropped": dropped}, nil)
}

// sitePage/report/index live in pages.go — the dashboard is
// server-rendered html/template now (no per-request string building).
