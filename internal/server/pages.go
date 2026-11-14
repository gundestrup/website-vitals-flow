// Package server renders the dashboard pages: one html/template shell
// (layout.tmpl) + shared partials + per-page "content" blocks cloned
// over a common base. Alpine hydrates these pages; it no longer owns
// card/finding/console/run markup — the same partials render it for
// both the site detail view and the standalone run report.
package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"math"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gundestrup/website-vitals-flow/internal/measure"
	"github.com/gundestrup/website-vitals-flow/internal/score"
	"github.com/gundestrup/website-vitals-flow/internal/store"
)

// tmplPaths resolves template names against static/ — or a dev dir
// when WVF_DEV_STATIC points at one (same override as s.static()).
func tmplPaths() (fs.FS, func(...string) []string) {
	if dir := os.Getenv("WVF_DEV_STATIC"); dir != "" {
		// #nosec G703 -- WVF_DEV_STATIC is an explicit local dev override.
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return os.DirFS(dir), func(n ...string) []string { return n }
		}
	}
	return staticFS, func(n ...string) []string {
		out := make([]string, len(n))
		for i, s := range n {
			out[i] = "static/" + s
		}
		return out
	}
}

// parseTemplates builds one *template.Template per page: each clone of
// the shared base (shell + partials) gets its own "content" block —
// the template name inside {{template "content" .}} is static, so the
// clone-per-page trick is the standard way to reuse one layout.
func parseTemplates() map[string]*template.Template {
	fsys, names := tmplPaths()
	base := template.Must(template.New("wvf").ParseFS(fsys,
		names("layout.tmpl", "partials.tmpl")...))
	pages := map[string]*template.Template{}
	for _, page := range []string{"index", "site", "report"} {
		t := template.Must(base.Clone())
		pages[page] = template.Must(t.ParseFS(fsys, names(page+".tmpl")...))
	}
	return pages
}

// render executes the shell for a page into a buffer (a bad template
// must not write a partial body before the 500).
func (s *Server) render(w http.ResponseWriter, page string, vm any) {
	var buf bytes.Buffer
	if err := s.tmpl[page].ExecuteTemplate(&buf, "shell", vm); err != nil {
		http.Error(w, "template error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set(hdrContentType, ctHTML)
	// nosemgrep -- template-escaped HTML rendered into buf above
	_, _ = w.Write(buf.Bytes())
}

// baseVM feeds the shared shell (layout.tmpl): Comp/Init pick the
// Alpine component the page hydrates with.
type baseVM struct {
	Title   string
	Eyebrow string
	Comp    string // Alpine x-data component: "dash" (interactive) or "hdr" (read-only)
	Init    string // x-init expression; "" = no init call
	Charts  bool   // include chart.js (site detail only)
}

// index renders the front page: sites list stays Alpine-driven
// (/api/sites fetch) — too interactive to server-render usefully.
func (s *Server) index(w http.ResponseWriter, _ *http.Request) {
	s.render(w, "index", baseVM{Title: "website-vitals-flow",
		Comp: "dash", Init: "load()"})
}

// sitePage renders one site's detail view: cards, screenshot,
// findings, console and the runs table are all server-side; Alpine
// owns charts, scan actions, the pages dropdown and job status.
func (s *Server) sitePage(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	site, err := s.st.SiteByKey(r.Context(), key)
	if err != nil {
		http.Error(w, errUnknownSite, http.StatusNotFound)
		return
	}
	runs, err := s.st.RunsForSite(r.Context(), site.ID, 100)
	if err != nil {
		http.Error(w, "runs", http.StatusInternalServerError)
		return
	}
	s.render(w, "site", siteView(site, runs))
}

// report renders one run's detail_json as a standalone HTML page —
// same view-model + partials as the site detail view.
func (s *Server) report(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	run, err := s.st.RunByID(r.Context(), id)
	if err != nil {
		http.Error(w, "unknown run", http.StatusNotFound)
		return
	}
	site, _ := s.st.SiteByID(r.Context(), run.SiteID)
	det := parseDetail(run.DetailJSON)
	vm := reportVM{
		baseVM: baseVM{Title: "wvf run report", Eyebrow: "run report",
			Comp: "hdr"},
		RunID: id,
		MetaLine: fmt.Sprintf("%s · engine %s · status %s · %s",
			run.StartedAt.Format("2006-01-02 15:04"), run.Engine,
			run.Status, ago(run.StartedAt)),
		Cards:      buildCards(run, det, ""),
		Shot:       shotCard{B64: shotB64(det), Wide: true},
		Findings:   findingVMs(det),
		Console:    consoleVMs(det),
		DetailJSON: prettyJSON(run.DetailJSON),
		BackHref:   "/",
	}
	if site != nil {
		vm.Key = site.Key
		vm.BackHref = "/site/" + url.PathEscape(site.Key)
	}
	if det != nil && det.URL != "" {
		if pu, perr := url.Parse(det.URL); perr == nil &&
			pu.Path != "" && pu.Path != "/" {
			vm.PagePath = strings.TrimPrefix(pu.Path, "/")
		}
	}
	s.render(w, "report", vm)
}

// siteVM is the site detail page's view-model.
type siteVM struct {
	baseVM
	Key      string
	HasRun   bool
	Cards    []metricCard
	Shot     shotCard
	Findings []findingVM
	Console  []consoleVM
	Runs     []runRow
}

// reportVM is the standalone run report's view-model.
type reportVM struct {
	baseVM
	Key        string
	BackHref   string
	RunID      int64
	PagePath   string
	MetaLine   string
	Cards      []metricCard
	Shot       shotCard
	Findings   []findingVM
	Console    []consoleVM
	DetailJSON string
}

// siteView mirrors the old SPA detail assembly: metric values come from
// the last ok run (falling back to the newest), detail_json-sourced
// widgets (proto/comp/tls/cache/console/screenshot/findings) come from
// the newest run — and cards get a stale stamp when the two diverge.
func siteView(site *store.Site, runs []store.Run) siteVM {
	vm := siteVM{
		baseVM: baseVM{Title: "wvf · " + site.Key, Eyebrow: "site",
			Comp: "dash", Init: "initSite()", Charts: true},
		Key:  site.Key,
		Runs: runRows(site.Key, runs),
	}
	var shown, newest *store.Run
	for i := range runs {
		if runs[i].Status == "ok" {
			shown = &runs[i]
			break
		}
	}
	if len(runs) > 0 {
		newest = &runs[0]
	}
	if shown == nil {
		shown = newest
	}
	if shown == nil {
		return vm
	}
	det := parseDetail(newest.DetailJSON)
	stale := ""
	if shown.ID != newest.ID {
		stale = ageShort(shown.StartedAt)
	}
	vm.HasRun = true
	vm.Cards = buildCards(shown, det, stale)
	vm.Shot = shotCard{B64: shotB64(det),
		Href: reportURL(site.Key, newest.ID)}
	vm.Findings = findingVMs(det)
	vm.Console = consoleVMs(det)
	return vm
}

func reportURL(key string, id int64) string {
	return "/site/" + url.PathEscape(key) + "/report/" +
		strconv.FormatInt(id, 10)
}

// parseDetail decodes a run's detail_json into the typed measure
// payload; a corrupt blob degrades the det-driven cards to "na".
func parseDetail(raw string) *measure.Result {
	var res measure.Result
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		return nil
	}
	return &res
}

// prettyJSON re-indents raw detail_json for the report's debug dump.
func prettyJSON(raw string) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(raw), "", "  "); err != nil {
		return raw
	}
	return buf.String()
}

// lastSample returns the final load sample (screenshot/console/dom
// live there) or nil.
func lastSample(d *measure.Result) *measure.Sample {
	if d == nil || len(d.Samples) == 0 {
		return nil
	}
	return &d.Samples[len(d.Samples)-1]
}

// --- view-model leaf types (template partials consume these) ---

type cardLine struct {
	Icon  string // "check" | "cross" | "" (no icon)
	Img   string // optional leading icon asset
	Label string
	Title string
}

type metricCard struct {
	ID    string // badge name prefix; "" = no badge image
	Band  string // good | ok | poor | na
	Text  string
	Label string
	Title string
	Age   string // stale stamp, e.g. "3h"; "" = fresh
	Lines []cardLine
}

type shotCard struct {
	B64  string // jpeg base64; "" = no card
	Wide bool   // mw-340 (report layout)
	Href string // optional report link (site detail)
}

type findingVM struct {
	Severity string
	Title    string
	Link     string
	Detail   string
}

type consoleVM struct {
	Kind  string
	Class string // sev-warn | sev-poor
	Text  string
}

type runRow struct {
	ID        int64
	When      string
	Engine    string
	TTFB      string
	LCP       string
	CLS       string
	TBT       string
	Proto     string
	ReportURL string
}

// --- card assembly (server port of the retired Alpine metricCards) ---

func bandOf(id string, v *float64) string {
	if v == nil {
		return "na"
	}
	if b := score.Metric(id, *v); b != "" {
		return string(b)
	}
	return "na"
}

func fmtMetric(id string, v *float64) string {
	if v == nil {
		return "-"
	}
	if id == "cls" {
		return fmt.Sprintf("%.3f", *v)
	}
	return fmt.Sprintf("%.0fms", *v)
}

// iconFor maps a bool to the check/cross icon name.
func iconFor(ok bool) string {
	if ok {
		return "check"
	}
	return "cross"
}

// cacheSignals is the httpcaching.com-style DOCUMENT header checklist —
// fixed order matches the old client-side list.
var cacheSignals = []struct{ key, title string }{
	{"cache-control", "Cache-Control header present"},
	{"expires", "Expires header present"},
	{"last-modified", "Last-Modified validator"},
	{"etag", "ETag validator"},
	{"vary", "Vary header"},
	{"age", "CDN/proxy age (cached)"},
	{"cf-cache-status", "Cloudflare cache status"},
	{"x-cache", "proxy cache status"},
	{"x-cache-hits", "cache hit counter"},
}

// buildCards renders the vitals grid: 5 headline metrics + page size,
// protocol, compression, TLS, cacheability and console widgets.
func buildCards(r *store.Run, d *measure.Result, stale string) []metricCard {
	var cards []metricCard
	ms := func(id string, v *float64, label string) {
		cards = append(cards, metricCard{ID: id, Band: bandOf(id, v),
			Text: fmtMetric(id, v), Label: label, Age: stale})
	}
	ms("ttfb", &r.TTFBms, "TTFB")
	ms("fcp", r.FCPms, "FCP")
	ms("lcp", r.LCPms, "LCP")
	ms("cls", &r.CLS, "CLS")
	ms("tbt", &r.TBTms, "TBT")
	return append(cards,
		pageSizeCard(r, d, stale), protoCard(r, d, stale),
		compCard(d, stale), tlsCard(d, stale), cacheCard(d, stale),
		consoleCard(d, stale))
}

// pageSizeCard: requests / weight / DOM per load
func pageSizeCard(r *store.Run, d *measure.Result, stale string) metricCard {
	dom := "-"
	if smp := lastSample(d); smp != nil {
		dom = fmt.Sprintf("%d nodes", smp.DOMNodes)
	}
	weight := "-"
	if r.PageBytes > 0 {
		weight = fmt.Sprintf("%dKB", int64(math.Round(float64(r.PageBytes)/1024)))
	}
	return metricCard{Band: "na", Label: "page size",
		Title: "requests / weight / DOM per load", Age: stale,
		Lines: []cardLine{
			{Label: fmt.Sprintf("%d requests", r.RequestCount),
				Title: "network requests per load"},
			{Img: "/icons/weight-svgrepo-com.svg", Label: weight,
				Title: "total transferred page weight"},
			{Img: "/icons/dom-svgrepo-com.svg", Label: dom,
				Title: "DOM node count"},
		}}
}

// protoCard: h3 advertised / h2 negotiated / h1.1 baseline
func protoCard(r *store.Run, d *measure.Result, stale string) metricCard {
	var h3adv bool
	var hcProto string
	if d != nil {
		if d.HTTP3 != nil {
			h3adv = d.HTTP3.Advertised
		}
		if d.HTTP != nil {
			hcProto = d.HTTP.Proto
		}
	}
	proto := r.HTTPProtocol
	h3ok := strings.HasPrefix(proto, "h3") || h3adv
	h2ok := h3ok || strings.HasPrefix(proto, "h2") || strings.Contains(hcProto, "2")
	h1ok := proto != "" || hcProto != ""
	nSup := 0
	for _, ok := range []bool{h3ok, h2ok, h1ok} {
		if ok {
			nSup++
		}
	}
	pband := "na"
	switch {
	case nSup >= 3:
		pband = "good"
	case nSup == 2:
		pband = "ok"
	case nSup == 1:
		pband = "poor"
	}
	return metricCard{ID: "proto", Band: pband,
		Label: "protocol", Age: stale,
		Lines: []cardLine{
			{Icon: iconFor(h3ok), Label: "HTTP/3", Title: "HTTP/3 (QUIC)"},
			{Icon: iconFor(h2ok), Label: "HTTP/2", Title: "HTTP/2"},
			{Icon: iconFor(h1ok), Label: "HTTP/1.1", Title: "HTTP/1.1 (baseline)"},
		}}
}

// compCard: compression encodings offered by the server
func compCard(d *measure.Result, stale string) metricCard {
	encOk := func(k string) bool {
		return d != nil && d.HTTP != nil &&
			d.HTTP.Encodings[k] != nil && d.HTTP.Encodings[k].OK
	}
	cband := "na"
	if encOk("br") {
		cband = "good"
	} else if encOk("gzip") {
		cband = "ok"
	}
	return metricCard{ID: "comp", Band: cband,
		Label: "compression", Age: stale,
		Title: "compression encodings offered by the server",
		Lines: []cardLine{
			{Img: "/icons/brotli-compression-icon.svg", Icon: iconFor(encOk("br")),
				Label: "br", Title: "Brotli"},
			{Img: "/icons/gzip-file-format-variant-svgrepo-com.svg", Icon: iconFor(encOk("gzip")),
				Label: "gzip", Title: "gzip"},
			{Img: "/icons/zstd.svg", Icon: iconFor(encOk("zstd")),
				Label: "zstd", Title: "Zstandard"},
		}}
}

// tlsCard: TLS version negotiated
func tlsCard(d *measure.Result, stale string) metricCard {
	tls := ""
	if d != nil && d.HTTP != nil {
		tls = d.HTTP.TLSVersion
	}
	tband := "na"
	if strings.Contains(tls, "1.3") {
		tband = "good"
	} else if tls != "" {
		tband = "ok"
	}
	ttext := tls
	if ttext == "" {
		ttext = "-"
	}
	return metricCard{ID: "tls", Band: tband, Text: ttext,
		Label: "TLS", Title: "TLS version negotiated", Age: stale}
}

// cacheCard: % of static assets — lines are the DOCUMENT page's
// cache/CDN header signals (a no-cache html is correct even when
// assets cache 100%).
func cacheCard(d *measure.Result, stale string) metricCard {
	var tot, cacheN int
	var sig map[string]bool
	if d != nil && d.HTTP != nil {
		for _, c := range d.HTTP.Cache {
			tot++
			if c.Cacheable {
				cacheN++
			}
		}
		sig = d.HTTP.Signals
	}
	pct := 0
	if tot > 0 {
		pct = int(math.Round(float64(cacheN) / float64(tot) * 100))
	}
	cacheBand := "na"
	cacheText := "-"
	if tot > 0 {
		switch {
		case pct >= 80:
			cacheBand = "good"
		case pct >= 50:
			cacheBand = "ok"
		default:
			cacheBand = "poor"
		}
		cacheText = fmt.Sprintf("%d%%", pct)
	}
	sigLines := make([]cardLine, 0, len(cacheSignals))
	for _, sl := range cacheSignals {
		sigLines = append(sigLines, cardLine{Icon: iconFor(sig[sl.key]),
			Label: sl.key, Title: sl.title})
	}
	return metricCard{ID: "cache", Band: cacheBand,
		Text: cacheText, Label: "cacheable", Age: stale,
		Title: "share of responses marked cacheable", Lines: sigLines}
}

// consoleCard: first messages as card lines; full list below the fold
func consoleCard(d *measure.Result, stale string) metricCard {
	var con []measure.ConsoleEntry
	if smp := lastSample(d); smp != nil {
		con = smp.Console
	}
	nErr := 0
	for _, c := range con {
		if c.Kind != "warn" {
			nErr++
		}
	}
	nWarn := len(con) - nErr
	conBand := "good"
	conText := "clean"
	if len(con) > 0 {
		if nErr > 0 {
			conBand = "poor"
			conText = fmt.Sprintf("%d err", nErr)
		} else {
			conBand = "ok"
			conText = fmt.Sprintf("%d warn", nWarn)
		}
	}
	conLines := make([]cardLine, 0, 3)
	for i, c := range con {
		if i >= 3 {
			break
		}
		icon := "cross"
		if c.Kind == "warn" {
			icon = "" // warn shows text only, like the old x-show pair
		}
		conLines = append(conLines, cardLine{Icon: icon,
			Label: truncate(c.Kind+": "+c.Text, 34), Title: c.Text})
	}
	return metricCard{Band: conBand, Text: conText,
		Label: "console", Age: stale, Lines: conLines,
		Title: "console messages captured during page load — click Console section below for all"}
}

// findingVMs dedups findings by id|title|detail — the same filter the
// SPA applied client-side.
func findingVMs(d *measure.Result) []findingVM {
	if d == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []findingVM
	for _, f := range d.Findings {
		k := f.ID + "|" + f.Title + "|" + f.Detail
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, findingVM{Severity: string(f.Severity),
			Title: f.Title, Link: f.Link, Detail: f.Detail})
	}
	return out
}

// consoleVMs exposes the last sample's console warn/error/exception
// entries — log/info noise is filtered at capture time.
func consoleVMs(d *measure.Result) []consoleVM {
	smp := lastSample(d)
	if smp == nil {
		return nil
	}
	var out []consoleVM
	for _, c := range smp.Console {
		class := "sev-poor"
		if c.Kind == "warn" {
			class = "sev-warn"
		}
		out = append(out, consoleVM{Kind: c.Kind, Class: class, Text: c.Text})
	}
	return out
}

func shotB64(d *measure.Result) string {
	if smp := lastSample(d); smp != nil {
		return smp.Screenshot
	}
	return ""
}

// runRows formats the history table (fmt = rounded ms, "-" when null).
func runRows(key string, runs []store.Run) []runRow {
	out := make([]runRow, 0, len(runs))
	for i := range runs {
		r := &runs[i]
		out = append(out, runRow{
			ID:        r.ID,
			When:      r.StartedAt.Format("2006-01-02 15:04"),
			Engine:    r.Engine,
			TTFB:      fmt.Sprintf("%.0f", r.TTFBms),
			LCP:       fmtOpt(r.LCPms),
			CLS:       fmt.Sprintf("%.3f", r.CLS),
			TBT:       fmt.Sprintf("%.0f", r.TBTms),
			Proto:     r.HTTPProtocol,
			ReportURL: reportURL(key, r.ID),
		})
	}
	return out
}

// truncate clips s to n runes (card line previews).
func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
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

// ageShort renders t like the retired JS age(): "Nm"/"Nh"/"Nd"/"Ny" —
// the stale stamp on cards when the shown metrics predate the newest
// run. Rounding is sequential (m→h→d→y) to match.
func ageShort(t time.Time) string {
	m := time.Since(t).Minutes()
	h := math.Round(m / 60)
	d := math.Round(h / 24)
	y := math.Round(d / 365)
	switch {
	case m < 60:
		return fmt.Sprintf("%dm", max(1, int(math.Round(m))))
	case h < 24:
		return fmt.Sprintf("%dh", int(h))
	case d < 365:
		return fmt.Sprintf("%dd", int(d))
	default:
		return fmt.Sprintf("%dy", int(y))
	}
}

// fmtOpt renders an optional metric as a rounded int or "-".
func fmtOpt(p *float64) string {
	if p == nil {
		return "-"
	}
	return fmt.Sprintf("%.0f", *p)
}
