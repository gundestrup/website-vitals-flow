// Package findings turns collected scan data into best-practice
// findings — the "what should I fix" layer. Runs on every scan
// (zero cost: pure analysis of already-collected data).
package findings

import (
	"fmt"
	"strings"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/deep"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/measure"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/psi"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/score"
)

// Finding is an alias to the measure type — findings produces them,
// measure owns the result shape.
type Finding = measure.Finding

// Severity is an alias to the measure severity scale.
type Severity = measure.Severity

// Severity levels re-exported for shorthand use in analyzers.
const (
	Info = measure.SevInfo
	Warn = measure.SevWarn
	Poor = measure.SevPoor
)

var links = map[string]string{
	"lcp":          "https://web.dev/articles/optimize-lcp",
	"tbt":          "https://web.dev/articles/optimize-tbt",
	"ttfb":         "https://web.dev/articles/optimize-ttfb",
	"cls":          "https://web.dev/articles/optimize-cls",
	"fcp":          "https://web.dev/articles/fcp",
	"encoding":     "https://web.dev/articles/optimizing-content-efficiency-http-caching",
	"brotli":       "https://developer.chrome.com/docs/performance/brotli",
	"cache":        "https://web.dev/articles/http-cache",
	"h2":           "https://web.dev/articles/performance-http2",
	"h3":           "https://developer.chrome.com/docs/performance/http3",
	"tls":          "https://web.dev/articles/why-https-matters",
	"requests":     "https://web.dev/articles/resource-count",
	"weight":       "https://web.dev/articles/reduce-network-payloads-using-text-compression",
	"domsize":      "https://developer.chrome.com/docs/lighthouse/performance/dom-size",
	"jsheap":       "https://developer.chrome.com/docs/devtools/memory-problems",
	"img_oversize": "https://web.dev/articles/serve-responsive-images",
	"img_format":   "https://web.dev/articles/serve-images-webp",
	"inp":          "https://web.dev/articles/optimize-inp",
}

// Analyze evaluates a completed measurement and returns findings,
// worst-severity first.
func Analyze(res *measure.Result) []Finding {
	var out []Finding
	if len(res.Samples) == 0 {
		return out
	}
	s := res.Samples[len(res.Samples)-1]

	vital := func(id string, v float64, goodAt, poorAt float64, unit, name string) {
		sev, ok := Severity(""), true
		switch {
		case v > poorAt:
			sev = Poor
		case v > goodAt:
			sev = Warn
		default:
			ok = false
		}
		if !ok {
			return
		}
		out = append(out, Finding{ID: id, Severity: sev, Category: "vitals",
			Title: fmt.Sprintf("%s above threshold (%.0f%s)", name, v, unit),
			Detail: fmt.Sprintf("good ≤ %.0f%s, poor > %.0f%s — see web.dev/vitals",
				goodAt, unit, poorAt, unit)})
	}
	vital("lcp", deref(s.LCPms), 2500, 4000, "ms", "LCP")
	vital("tbt", s.TBTms, 200, 600, "ms", "TBT")
	vital("ttfb", s.TTFBms, 800, 1800, "ms", "TTFB")
	if s.CLS > 0.1 {
		sev := Warn
		if s.CLS > 0.25 {
			sev = Poor
		}
		d := "good ≤ 0.1, poor > 0.25"
		if res.Attribution != nil && len(res.Attribution.CLSItems) > 0 {
			d += " — largest shift: " + strings.Join(res.Attribution.CLSItems, ", ")
		}
		out = append(out, Finding{ID: "cls", Severity: sev, Category: "vitals",
			Title:  fmt.Sprintf("layout shift %.3f", s.CLS),
			Detail: d})
	}

	// --- compression: text assets arriving uncompressed ---
	var unenc []string
	for _, r := range s.Responses {
		if isText(r.MimeType) && r.ContentEncoding == "" && r.EncodedBytes > 1024 {
			unenc = append(unenc, short(r.URL))
		}
	}
	if len(unenc) > 0 {
		out = append(out, Finding{ID: "encoding", Severity: Warn, Category: "compression",
			Title:  fmt.Sprintf("%d text responses served uncompressed", len(unenc)),
			Detail: "e.g. " + strings.Join(unenc[:minI(3, len(unenc))], ", ")})
	}
	if res.HTTP != nil {
		br := res.HTTP.Encodings["br"]
		if br != nil && br.Returned == "" && br.Error == "" {
			out = append(out, Finding{ID: "brotli", Severity: Info, Category: "compression",
				Title:  "brotli not negotiated",
				Detail: "server responds to br offer without content-encoding — br saves ~15-20% over gzip on text"})
		}
	}

	// --- caching: static assets without cacheability ---
	var nocache []string
	if res.HTTP != nil {
		for _, c := range res.HTTP.Cache {
			if !c.Cacheable && isStatic(c.URL) {
				nocache = append(nocache, short(c.URL)+" ("+strings.Join(c.Reasons, "; ")+")")
			}
		}
	}
	if len(nocache) > 0 {
		out = append(out, Finding{ID: "cache", Severity: Warn, Category: "cache",
			Title:  fmt.Sprintf("%d static assets not cacheable", len(nocache)),
			Detail: strings.Join(nocache[:minI(3, len(nocache))], " · ")})
	}

	// --- protocol ---
	if len(s.Responses) > 0 {
		doc := s.Responses[0]
		if doc.Protocol == "http/1.0" || doc.Protocol == "http/1.1" {
			out = append(out, Finding{ID: "h2", Severity: Info, Category: "protocol",
				Title:  "document served over " + doc.Protocol,
				Detail: "h2/h3 enable multiplexing — consider TLS + h2 at minimum"})
		}
	}
	if res.HTTP3 != nil && !res.HTTP3.Advertised && res.HTTP != nil && res.HTTP.TLSVersion != "" {
		out = append(out, Finding{ID: "h3", Severity: Info, Category: "protocol",
			Title:  "HTTP/3 not advertised",
			Detail: "no alt-svc h3 — QUIC cuts handshake cost on lossy/mobile links"})
	}
	if strings.HasPrefix(res.URL, "http://") && !isLocal(res.URL) {
		out = append(out, Finding{ID: "tls", Severity: Warn, Category: "protocol",
			Title:  "served without TLS",
			Detail: "https required for h2/h3, brotli, and modern browser features"})
	}

	// --- images: oversized serving + legacy formats ---
	var oversized, legacy []string
	for _, im := range s.Images {
		if im.NatW > 0 && im.DispW > 0 && im.NatW > 2*im.DispW && im.Bytes > 40<<10 {
			oversized = append(oversized,
				fmt.Sprintf("%dx%d shown at %dx%d (%s)",
					im.NatW, im.NatH, im.DispW, im.DispH, short(im.Src)))
		}
		f := strings.ToLower(im.Format)
		if (strings.Contains(f, "jpeg") || strings.Contains(f, "png") ||
			strings.HasSuffix(im.Src, ".jpg") || strings.HasSuffix(im.Src, ".png")) &&
			im.Bytes > 20<<10 {
			legacy = append(legacy, fmt.Sprintf("%s (%s, %d KB)",
				short(im.Src), im.Format, im.Bytes>>10))
		}
	}
	if len(oversized) > 0 {
		out = append(out, Finding{ID: "img_oversize", Severity: Warn, Category: "images",
			Title: fmt.Sprintf("%d images served larger than displayed", len(oversized)),
			Detail: strings.Join(oversized[:minI(3, len(oversized))], " · ") +
				" — resize or srcset"})
	}
	if len(legacy) > 0 {
		out = append(out, Finding{ID: "img_format", Severity: Info, Category: "images",
			Title: fmt.Sprintf("%d images in jpeg/png — avif/webp ~30-50%% smaller", len(legacy)),
			Detail: strings.Join(legacy[:minI(3, len(legacy))], " · ") +
				" — serve via <picture> with format fallback"})
	}

	// --- page weight / DOM / JS ---
	if len(s.Responses) > 75 {
		out = append(out, Finding{ID: "requests", Severity: Warn, Category: "dom",
			Title:  fmt.Sprintf("%d requests", len(s.Responses)),
			Detail: "consider bundling, HTTP/2 push→preload, lazy-loading"})
	}
	var bytes int64
	for _, r := range s.Responses {
		bytes += r.EncodedBytes
	}
	if bytes > 2<<20 {
		out = append(out, Finding{ID: "weight", Severity: Warn, Category: "dom",
			Title:  fmt.Sprintf("page weight %.1f MB", float64(bytes)/(1<<20)),
			Detail: "aim < 1.5 MB transferred for good mobile lab scores"})
	}
	if s.DOMNodes > 1500 {
		out = append(out, Finding{ID: "domsize", Severity: Warn, Category: "dom",
			Title:  fmt.Sprintf("%d DOM nodes", s.DOMNodes),
			Detail: "large DOMs slow layout/style recalc — aim < ~1400"})
	}
	if s.JSHeap > 32<<20 {
		out = append(out, Finding{ID: "jsheap", Severity: Warn, Category: "js",
			Title:  fmt.Sprintf("JS heap %.0f MB", float64(s.JSHeap)/(1<<20)),
			Detail: "heavy JS memory usage correlates with long tasks"})
	}

	for i := range out {
		out[i].Link = links[out[i].ID]
	}
	// severity sort: poor > warn > info
	order := map[Severity]int{Poor: 0, Warn: 1, Info: 2}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if order[out[j].Severity] < order[out[i].Severity] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	// --- JS/CSS delivery budgets (best practice) ---
	var jsB, cssB int64
	var jsN, cssN int
	var bigJS, bigCSS []string
	for _, r := range s.Responses {
		u := strings.ToLower(r.URL)
		js := strings.Contains(r.MimeType, "javascript") || strings.HasSuffix(u, ".js")
		css := strings.Contains(r.MimeType, "css") || strings.HasSuffix(u, ".css")
		switch {
		case js:
			jsN++
			jsB += r.EncodedBytes
			if r.EncodedBytes > 500_000 {
				bigJS = append(bigJS, short(r.URL))
			}
		case css:
			cssN++
			cssB += r.EncodedBytes
			if r.EncodedBytes > 200_000 {
				bigCSS = append(bigCSS, short(r.URL))
			}
		}
	}
	if jsB > 1_500_000 {
		out = append(out, Finding{ID: "js_weight", Severity: measure.SevWarn, Category: "js",
			Title: fmt.Sprintf("page ships %.1f MB of JavaScript across %d files", float64(jsB)/1e6, jsN),
			Link:  "https://web.dev/articles/optimize-long-tasks"})
	}
	for _, u := range bigJS {
		out = append(out, Finding{ID: "js_bundle", Severity: measure.SevWarn, Category: "js",
			Title:  "very large JS bundle: " + u,
			Detail: "split/lazy-load; single bundles >500KB inflate parse+eval and TBT",
			Link:   "https://web.dev/articles/reduce-javascript-payloads-with-code-splitting"})
	}
	if cssB > 300_000 {
		out = append(out, Finding{ID: "css_weight", Severity: measure.SevWarn, Category: "css",
			Title: fmt.Sprintf("page ships %d KB of CSS across %d files", cssB/1024, cssN),
			Link:  "https://web.dev/articles/reduce-unneeded-javascript"})
	}
	for _, u := range bigCSS {
		out = append(out, Finding{ID: "css_bundle", Severity: measure.SevInfo, Category: "css",
			Title: "large stylesheet: " + u,
			Link:  "https://web.dev/articles/extract-critical-css"})
	}
	if jsN > 15 {
		out = append(out, Finding{ID: "js_count", Severity: measure.SevInfo, Category: "js",
			Title: fmt.Sprintf("%d JS files — many bundles", jsN),
			Link:  "https://web.dev/articles/reduce-javascript-payloads-with-code-splitting"})
	}

	// --- site basics: conventional files (webserver-level best practice)
	if res.HTTP != nil {
		for path, st := range res.HTTP.Basics {
			if st == 0 || st >= 400 {
				out = append(out, Finding{ID: "basics_" + strings.TrimPrefix(path, "/"),
					Severity: measure.SevInfo, Category: "server",
					Title:  "missing " + path,
					Detail: fmt.Sprintf("returned status %d — expected by crawlers/agents", st),
					Link:   "https://developers.google.com/search/docs/crawling-indexing/robots/intro"})
			}
		}
	}

	return out
}

// ParseOnly turns "lcp,cls,http" into a set. Empty = all stages.
func ParseOnly(csv string) map[string]bool {
	if csv == "" {
		return nil
	}
	out := map[string]bool{}
	for _, p := range strings.Split(csv, ",") {
		out[strings.TrimSpace(p)] = true
	}
	return out
}

// Want reports whether a pipeline stage runs under the only-set.
func Want(only map[string]bool, stage string) bool {
	// nil or EMPTY set = run everything ('deep' alone also means
	// full scan + deep stage, not narrowing).
	if len(only) == 0 || (len(only) == 1 && only["deep"]) {
		return true
	}
	return only[stage]
}

// Filter drops findings not in the only-set (matched by id or category).
func Filter(fs []Finding, only map[string]bool) []Finding {
	if only == nil {
		return fs
	}
	var out []Finding
	for _, f := range fs {
		if only[f.ID] || only[f.Category] {
			out = append(out, f)
		}
	}
	return out
}

func isText(m string) bool {
	return strings.HasPrefix(m, "text/") || strings.Contains(m, "javascript") ||
		strings.Contains(m, "json") || strings.Contains(m, "svg") || m == ""
}
func isStatic(u string) bool {
	return strings.Contains(u, ".css") || strings.Contains(u, ".js") ||
		strings.Contains(u, ".woff") || strings.Contains(u, ".png") ||
		strings.Contains(u, ".jpg") || strings.Contains(u, ".svg") ||
		strings.Contains(u, "/assets/") || strings.Contains(u, "/dist/")
}
func isLocal(u string) bool {
	return strings.Contains(u, "localhost") || strings.Contains(u, "127.0.0.1") ||
		strings.Contains(u, "::1")
}
func short(u string) string {
	if len(u) > 70 {
		return u[:67] + "..."
	}
	return u
}
func deref(f *float64) float64 {
	if f == nil {
		return 0
	}
	return *f
}
func minI(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// FromLighthouse converts Lighthouse audit failures into findings,
// preserving their web.dev doc links. Severity: score<0.5 poor,
// else warn. d carries the display value + estimated savings.
func FromLighthouse(lh *deep.LHReport) []measure.Finding {
	if lh == nil {
		return nil
	}
	var out []measure.Finding
	for _, f := range lh.Findings {
		sev := measure.SevWarn
		if f.Score < 0.5 {
			sev = measure.SevPoor
		}
		d := f.Display
		if f.Savings > 0 {
			d = strings.TrimSpace(d + fmt.Sprintf(" (~%.0fms)", f.Savings))
		}
		if f.SavingsB > 0 {
			d = strings.TrimSpace(d + fmt.Sprintf(" (~%.0fKB)", f.SavingsB/1024))
		}
		if len(f.Items) > 0 {
			d = strings.TrimSpace(d + "\n" + strings.Join(f.Items, "\n"))
		}
		out = append(out, measure.Finding{
			ID: "lh_" + f.ID, Severity: sev, Category: "lighthouse",
			Title: f.Title, Detail: d, Link: f.Link,
		})
	}
	return out
}

// FromAttribution surfaces the web-vitals debugger payload as info
// findings — LCP element, CLS shift sources, worst INP target.
func FromAttribution(a *deep.Attribution) []measure.Finding {
	if a == nil {
		return nil
	}
	var out []measure.Finding
	info := func(id, title, detail, link string) {
		out = append(out, measure.Finding{ID: id, Severity: measure.SevInfo,
			Category: "attribution", Title: title, Detail: detail, Link: link})
	}
	if a.LCPElement != "" {
		info("attr_lcp", "LCP element", a.LCPElement+" "+a.LCPURL,
			"https://web.dev/articles/optimize-lcp")
	}
	if len(a.LCPSubparts) > 0 {
		info("attr_lcp_parts", "LCP phase breakdown",
			strings.Join(a.LCPSubparts, " · "),
			"https://web.dev/articles/optimize-lcp#lcp-breakdown")
	}
	for _, it := range a.CLSItems {
		info("attr_cls", "CLS shift source", it,
			"https://web.dev/articles/optimize-cls")
	}
	if a.INPms != nil {
		info("attr_inp", "Worst interaction (lab-INP)",
			fmt.Sprintf("%.0fms %s %s", *a.INPms, a.INPType, a.INPTarget),
			"https://web.dev/articles/inp")
	}
	return out
}

// FromPSI surfaces CrUX field p75 + lab vitals as findings when PSI
// data exists (public FQDN + quota only).
func FromPSI(p *psi.Result) []measure.Finding {
	if p == nil {
		return nil
	}
	var out []measure.Finding
	if p.FieldData {
		field := map[string]string{
			"LARGEST_CONTENTFUL_PAINT_MS":  "lcp",
			"FIRST_CONTENTFUL_PAINT_MS":    "fcp",
			"TIME_TO_FIRST_BYTE_MS":        "ttfb",
			"INTERACTION_TO_NEXT_PAINT_MS": "inp",
		}
		for k, id := range field {
			if v, ok := p.P75[k]; ok {
				sev := sevOf(score.Metric(id, v))
				out = append(out, measure.Finding{
					ID: "psi_" + id, Severity: sev, Category: "psi-field",
					Title:  "CrUX p75 " + id,
					Detail: fmt.Sprintf("%.0fms real-user", v),
					Link:   "https://web.dev/articles/vitals"})
			}
		}
		if v, ok := p.P75["CUMULATIVE_LAYOUT_SHIFT_SCORE"]; ok {
			sev := sevOf(score.Metric("cls", v/100))
			out = append(out, measure.Finding{ID: "psi_cls", Severity: sev,
				Category: "psi-field", Title: "CrUX p75 cls",
				Detail: fmt.Sprintf("%.2f real-user", v/100),
				Link:   "https://web.dev/articles/cls"})
		}
	}
	if p.PerfScore > 0 && p.PerfScore < 90 {
		out = append(out, measure.Finding{ID: "psi_score",
			Severity: measure.SevWarn, Category: "psi-lab",
			Title: fmt.Sprintf("PSI performance score %.0f", p.PerfScore),
			Link:  "https://developer.chrome.com/docs/lighthouse/performance/performance-scoring"})
	}
	return out
}

func sevOf(b score.Band) measure.Severity {
	switch b {
	case score.Poor:
		return measure.SevPoor
	case score.OK:
		return measure.SevWarn
	}
	return measure.SevInfo
}

// Dedup drops findings identical by id+title+detail (merge order
// between deep collect and post-persist can double-append).
func Dedup(fs []Finding) []Finding {
	seen := map[string]bool{}
	out := fs[:0]
	for _, f := range fs {
		k := f.ID + "|" + f.Title + "|" + f.Detail
		if !seen[k] {
			seen[k] = true
			out = append(out, f)
		}
	}
	return out
}
