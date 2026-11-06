// Package server hosts the wvf dashboard: JSON API + embedded static UI.
package server

import (
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"time"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/config"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/jobs"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/score"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/sitekey"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/store"
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

func (s *Server) index(w http.ResponseWriter, _ *http.Request) {
	b, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, "index missing", 500)
		return
	}
	w.Header().Set(hdrContentType, ctHTML)
	_, _ = w.Write(b) // nosemgrep -- static embedded index
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
		writeJSON(w, map[string]string{"error": errUnknownSite}, err)
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
	site, _ := s.st.SiteByID(r.Context(), run.SiteID)
	var det map[string]any
	_ = json.Unmarshal([]byte(run.DetailJSON), &det)

	var b strings.Builder
	reportHeader(&b, site, run, id)
	reportMetrics(&b, run)
	reportScreenshot(&b, det)
	reportFindings(&b, det)
	reportConsole(&b, det)
	reportDetail(&b, det)
	b.WriteString(`</section>`)
	w.Header().Set(hdrContentType, ctHTML)
	_, _ = w.Write([]byte(b.String())) // nosemgrep -- interpolated values html.EscapeString'd
}

// ago renders t as "Nm ago" / "Nh ago" / "Nd ago".
func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

// fmtOpt renders an optional metric as a rounded int or "-".
func fmtOpt(p *float64) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprintf("%.0f", *p)
}

// optVal unwraps an optional metric — 0 when absent.
func optVal(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

// metricCell renders one badge + value + label grid cell.
func metricCell(metric, val, label string, v float64) string {
	band := "na"
	if metric != "" {
		band = string(score.Metric(metric, v))
	}
	return fmt.Sprintf(`<div class=metric-cell><img class=badge-img src="/badges/%s-%s.svg"><div class="metric-val mono">%s</div><div class=metric-lbl>%s</div></div>`,
		metric, band, val, label)
}

// lastSample returns the final sample object from detail_json.
func lastSample(det map[string]any) (map[string]any, bool) {
	sm, ok := det["samples"].([]any)
	if !ok || len(sm) == 0 {
		return nil, false
	}
	m, ok := sm[len(sm)-1].(map[string]any)
	return m, ok
}

// reportHeader writes the doctype, backlink, title, and meta line.
func reportHeader(b *strings.Builder, site *store.Site, run *store.Run, id int64) {
	b.WriteString(`<!doctype html><meta charset=utf-8><meta name=viewport content="width=device-width,initial-scale=1">`)
	b.WriteString(`<link rel=stylesheet href=/style.css><title>wvf run report</title><section class=section>`)
	b.WriteString(`<p class=eyebrow>website-vitals-flow · run report</p>`)
	key := ""
	if site != nil {
		key = site.Key
	}
	fmt.Fprintf(b, `<a href="/site/%s" class=mono style="font-size:.8rem">&larr; %s</a>`,
		url.PathEscape(key), html.EscapeString(key))
	fmt.Fprintf(b, `<h1 class="title is-4" style="font-family:var(--wvf-serif)">Run %d</h1>`, id)
	fmt.Fprintf(b, `<p class=mono style="font-size:.78rem;color:var(--wvf-muted)">%s &middot; engine %s &middot; status %s &middot; %s</p>`,
		run.StartedAt.Format("2006-01-02 15:04"), run.Engine, run.Status, ago(run.StartedAt))
}

// reportMetrics writes the vitals + size metric grid.
func reportMetrics(b *strings.Builder, run *store.Run) {
	b.WriteString(`<div class=metric-grid>`)
	b.WriteString(metricCell("ttfb", fmt.Sprintf("%.0fms", run.TTFBms), "TTFB", run.TTFBms))
	b.WriteString(metricCell("fcp", fmtOpt(run.FCPms)+"ms", "FCP", optVal(run.FCPms)))
	b.WriteString(metricCell("lcp", fmtOpt(run.LCPms)+"ms", "LCP", optVal(run.LCPms)))
	b.WriteString(metricCell("cls", fmt.Sprintf("%.3f", run.CLS), "CLS", run.CLS))
	b.WriteString(metricCell("tbt", fmt.Sprintf("%.0f", run.TBTms)+"ms", "TBT", run.TBTms))
	fmt.Fprintf(b, `<div class=metric-cell><div class="metric-val mono">%d</div><div class=metric-lbl>requests</div></div>`, run.RequestCount)
	fmt.Fprintf(b, `<div class=metric-cell><div class="metric-val mono">%dKB</div><div class=metric-lbl>weight</div></div>`, run.PageBytes/1024)
	b.WriteString(`</div>`)
}

// reportScreenshot writes the last sample's screenshot card, if any.
func reportScreenshot(b *strings.Builder, det map[string]any) {
	if m, ok := lastSample(det); ok {
		if shot, ok := m["screenshot"].(string); ok && shot != "" {
			fmt.Fprintf(b, `<div class=shot-card style="max-width:340px"><div class=metric-lbl style="padding:.4em .6em .2em">page after load</div><img src="data:image/jpeg;base64,%s"></div>`, shot)
		}
	}
}

// reportFindings writes the findings section, if any.
func reportFindings(b *strings.Builder, det map[string]any) {
	fs, ok := det["findings"].([]any)
	if !ok || len(fs) == 0 {
		return
	}
	b.WriteString(`<h2 class=section-title>Findings</h2>`)
	for _, fi := range fs {
		m, _ := fi.(map[string]any)
		b.WriteString(`<div class=finding>`)
		fmt.Fprintf(b, `<b class="%v">[%v]</b> %v`, m["severity"], m["severity"], html.EscapeString(fmt.Sprint(m["title"])))
		if m["link"] != nil {
			fmt.Fprintf(b, ` <a href="%v" target=_blank rel=noopener>&rarr; guide</a>`, html.EscapeString(fmt.Sprint(m["link"])))
		}
		if d, ok := m["detail"].(string); ok && d != "" {
			fmt.Fprintf(b, `<div class=mono style="font-size:.72rem;color:var(--wvf-muted);white-space:pre-line;padding-left:1.2em">%v</div>`, html.EscapeString(d))
		}
		b.WriteString(`</div>`)
	}
}

// reportConsole writes the last sample's console warn/error/exception
// entries, if any.
func reportConsole(b *strings.Builder, det map[string]any) {
	m, ok := lastSample(det)
	if !ok {
		return
	}
	cl, ok := m["console"].([]any)
	if !ok || len(cl) == 0 {
		return
	}
	b.WriteString(`<h2 class=section-title>Console</h2>`)
	for _, ce := range cl {
		c, _ := ce.(map[string]any)
		cls := "sev-poor"
		if c["kind"] == "warn" {
			cls = "sev-warn"
		}
		fmt.Fprintf(b, `<div class=finding><b class="%v">[%v]</b> <span class=mono style="font-size:.78rem">%v</span></div>`,
			cls, c["kind"], html.EscapeString(fmt.Sprint(c["text"])))
	}
}

// reportDetail dumps the raw detail_json for debugging.
func reportDetail(b *strings.Builder, det map[string]any) {
	j, _ := json.MarshalIndent(det, "", "  ")
	fmt.Fprintf(b, `<h2 class=section-title>detail_json</h2><pre style="background:var(--wvf-bg-soft);padding:1em;overflow:auto;font-size:.75em;border-radius:6px">%s</pre>`, j)
}
