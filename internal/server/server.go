// Package server hosts the wvf dashboard: JSON API + embedded static UI.
package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/config"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/jobs"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/score"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/sitekey"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/store"
	"time"
)

//go:embed static
var staticFS embed.FS

// Server bundles deps for handlers.
type Server struct {
	st     *store.Store
	cfg    config.Config
	runner *jobs.Runner
}

// New builds the handler. WVF_DEV_STATIC=<dir> serves assets from disk.
func New(st *store.Store, cfg config.Config, r *jobs.Runner) http.Handler {
	s := &Server{st: st, cfg: cfg, runner: r}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sites", s.sites)
	mux.HandleFunc("GET /api/sites/{key}/runs", s.runs)
	mux.HandleFunc("GET /api/runs/{id}", s.run)
	mux.HandleFunc("GET /api/jobs", s.jobs)
	mux.HandleFunc("GET /api/jobs/{id}", s.job)
	mux.HandleFunc("GET /badge.svg", s.badge)
	// SPA route: /site/<key> serves the same index (':' is legal in
	// a path segment, so /site/localhost:8473 works directly)
	mux.HandleFunc("GET /site/{key}", s.index)
	mux.HandleFunc("GET /site/{key}/report/{id}", s.report)
	mux.HandleFunc("POST /api/scan", s.scan)
	mux.HandleFunc("POST /api/scan-now", s.scanNow)
	mux.HandleFunc("POST /api/sites", s.siteAdd)
	mux.HandleFunc("PATCH /api/sites/{key}", s.siteEdit)
	mux.HandleFunc("DELETE /api/sites/{key}", s.siteDelete)
	mux.HandleFunc("POST /api/prune", s.prune)
	mux.HandleFunc("GET /report/{id}", s.report)
	mux.Handle("GET /", s.static())
	return headers(mux)
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	b, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "index missing", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(b)
}

func (s *Server) static() http.Handler {
	var fsys http.FileSystem
	if dir := os.Getenv("WVF_DEV_STATIC"); dir != "" {
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
		writeJSON(w, map[string]string{"error": "unknown site"}, err)
		return
	}
	runs, err := s.st.RunsForSite(r.Context(), site.ID, 100)
	writeJSON(w, runs, err)
}

func (s *Server) run(w http.ResponseWriter, r *http.Request) {
	var id int64
	if _, err := fmt_Sscanf(r.PathValue("id"), &id); err != nil {
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

func (s *Server) scanNow(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key  string `json:"key"`
		Deep bool   `json:"deep"`
		Only string `json:"only"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, nil, err)
		return
	}
	site, err := s.st.SiteByKey(r.Context(), body.Key)
	if err != nil {
		writeJSON(w, map[string]string{"error": "unknown site"}, err)
		return
	}
	// request-level overrides fall back to the site's own settings
	only := body.Only
	if only == "" {
		only = site.Only
	}
	id, err := s.runner.Enqueue(r.Context(), site.ID, "scan", body.Deep || site.Deep, only)
	writeJSON(w, map[string]int64{"job": id}, err)
}

func fmt_Sscanf(s string, id *int64) (int, error) {
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
		if _, err := fmt_Sscanf(id, &rid); err == nil {
			if run, err := s.st.RunByID(r.Context(), rid); err == nil {
				value, ok = runMetric(run, metric)
			}
		}
	}
	if !ok {
		// raw value mode: ?value=532 or ?cls=0.2
		if v := q.Get("value"); v != "" {
			var f float64
			if _, err := fmt_Sscanfloat(v, &f); err == nil {
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
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=300")
	w.Write([]byte(score.BadgeSVG(label, vtext, band)))
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

func fmt_Sscanfloat(s string, f *float64) (int, error) {
	var neg bool
	i := 0
	if len(s) > 0 && s[0] == '-' {
		neg = true
		i = 1
	}
	var intp, frac, fracdiv float64
	fracdiv = 1
	seen := false
	for ; i < len(s); i++ {
		c := s[i]
		if c == '.' {
			continue
		}
		if c < '0' || c > '9' {
			return 0, errBadID
		}
		seen = true
		if fracdiv == 1 {
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
	if body.Wait > 300 {
		body.Wait = 300
	}
	deadline := time.Now().Add(time.Duration(body.Wait) * time.Second)
	for time.Now().Before(deadline) {
		j, runID, err := s.st.JobByID(r.Context(), jobID)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		resp["state"] = j.State
		resp["detail"] = j.Detail
		if j.State == "done" || j.State == "failed" {
			if runID > 0 {
				run, rerr := s.st.RunByID(r.Context(), runID)
				if rerr == nil {
					resp["run_id"] = runID
					resp["run"] = run
				}
			}
			writeJSON(w, resp, nil)
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

// job returns one job + its run id for polling clients.
func (s *Server) job(w http.ResponseWriter, r *http.Request) {
	var id int64
	if _, err := fmt_Sscanf(r.PathValue("id"), &id); err != nil {
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
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		v = map[string]string{"error": err.Error()}
	}
	json.NewEncoder(w).Encode(v)
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
	site, err := s.st.UpsertSite(r.Context(), key, canon, b.Label, b.Schedule,
		s.cfg.KeepLast, s.cfg.MaxAgeDays, b.Deep, b.Only)
	writeJSON(w, site, err)
}

// siteEdit patches a monitored site's label/schedule/deep/only.
func (s *Server) siteEdit(w http.ResponseWriter, r *http.Request) {
	site, err := s.st.SiteByKey(r.Context(), r.PathValue("key"))
	if err != nil {
		writeJSON(w, map[string]string{"error": "unknown site"}, nil)
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
	updated, err := s.st.UpsertSite(r.Context(), site.Key, site.URL, label,
		sched, site.KeepLast, site.MaxAgeDays, deep, only)
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

// report renders one run's detail_json as a standalone HTML page.
func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", 400)
		return
	}
	run, err := s.st.RunByID(r.Context(), id)
	if err != nil {
		http.Error(w, "unknown run", 404)
		return
	}
	var det map[string]any
	_ = json.Unmarshal([]byte(run.DetailJSON), &det)
	var b strings.Builder
	b.WriteString("<!doctype html><meta charset=utf-8><title>wvf run report</title>")
	b.WriteString("<style>body{font-family:system-ui;max-width:900px;margin:2em auto;padding:0 1em}")
	b.WriteString("table{border-collapse:collapse}td,th{border:1px solid #ccc;padding:.3em .6em;font-size:.85em}")
	b.WriteString(".poor{color:#c62828}.warn{color:#b26a00}.info{color:#555}pre{background:#f4f1e8;padding:1em;overflow:auto;font-size:.75em}</style>")
	fmt.Fprintf(&b, "<h1>Run %d</h1><p>%s · engine %s · %s</p>", id, run.BrowserVersion, run.Engine, run.StartedAt.Format("2006-01-02 15:04"))
	b.WriteString("<table><tr><th>ttfb</th><th>fcp</th><th>lcp</th><th>cls</th><th>tbt</th><th>reqs</th></tr>")
	f := func(p *float64) string {
		if p == nil {
			return "-"
		}
		return fmt.Sprintf("%.0f", *p)
	}
	fmt.Fprintf(&b, "<tr><td>%.0f</td><td>%s</td><td>%s</td><td>%.3f</td><td>%.0f</td><td>%d</td></tr></table>",
		run.TTFBms, f(run.FCPms), f(run.LCPms), run.CLS, run.TBTms, run.RequestCount)
	if fs, ok := det["findings"].([]any); ok && len(fs) > 0 {
		b.WriteString("<h2>Findings</h2>")
		for _, fi := range fs {
			m := fi.(map[string]any)
			fmt.Fprintf(&b, "<p class=%v>[%v] %v", m["severity"], m["severity"], m["title"])
			if m["link"] != nil {
				fmt.Fprintf(&b, ` — <a href="%v">guide</a>`, m["link"])
			}
			if m["detail"] != nil {
				fmt.Fprintf(&b, "<br><small>%v</small>", m["detail"])
			}
			b.WriteString("</p>")
		}
	}
	j, _ := json.MarshalIndent(det, "", "  ")
	fmt.Fprintf(&b, "<h2>detail_json</h2><pre>%s</pre>", j)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(b.String()))
}
