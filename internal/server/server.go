// Package server hosts the wvf dashboard: JSON API + embedded static UI.
package server

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/config"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/jobs"
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
	mux.HandleFunc("POST /api/scan", s.scan)
	mux.HandleFunc("POST /api/scan-now", s.scanNow)
	mux.Handle("GET /", s.static())
	return headers(mux)
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
		Key string `json:"key"`
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
	id, err := s.runner.Enqueue(r.Context(), site.ID, "scan")
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

// scan is the IDE/agent entry point: POST {"url": ..., "wait": seconds}.
// Normalizes the URL to a site key (created on first sight), enqueues,
// optionally waits for the result. Response: {"job", "site", "run"?}.
func (s *Server) scan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL  string `json:"url"`
		Wait int    `json:"wait"`
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
	jobID, err := s.runner.Enqueue(r.Context(), site.ID, "scan")
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
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
