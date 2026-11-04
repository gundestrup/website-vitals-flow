// Package findings turns collected scan data into best-practice
// findings — the "what should I fix" layer. Runs on every scan
// (zero cost: pure analysis of already-collected data).
package findings

import (
	"fmt"
	"strings"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/measure"
)

// Finding is an alias to the measure type — findings produces them,
// measure owns the result shape.
type Finding = measure.Finding
type Severity = measure.Severity

const (
	Info = measure.SevInfo
	Warn = measure.SevWarn
	Poor = measure.SevPoor
)

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
		} else if s.LCPElem != "" {
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

	// severity sort: poor > warn > info
	order := map[Severity]int{Poor: 0, Warn: 1, Info: 2}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if order[out[j].Severity] < order[out[i].Severity] {
				out[i], out[j] = out[j], out[i]
			}
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
