// Package measure drives one page load and extracts vitals + network data.
package measure

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/performance"
	"github.com/chromedp/chromedp"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/deep"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/http3"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/httpcheck"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/psi"
)

// Severity ranks findings: info < warn < poor.
type Severity string

const (
	SevInfo Severity = "info"
	SevWarn Severity = "warn"
	SevPoor Severity = "poor"
)

// ImageInfo is one rendered <img>: natural vs displayed size.
type ImageInfo struct {
	Src    string `json:"src"`
	NatW   int    `json:"natural_width"`
	NatH   int    `json:"natural_height"`
	DispW  int    `json:"display_width"`
	DispH  int    `json:"display_height"`
	Bytes  int64  `json:"bytes,omitempty"` // joined from response when known
	Format string `json:"format,omitempty"`
}

// Finding is one best-practice issue + evidence + recommendation.
type Finding struct {
	ID       string   `json:"id"`
	Severity Severity `json:"severity"`
	Category string   `json:"category"`
	Title    string   `json:"title"`
	Detail   string   `json:"detail"`
	Link     string   `json:"link,omitempty"` // how to improve
}

// Response is one observed network response.
type Response struct {
	URL             string            `json:"url"`
	Protocol        string            `json:"protocol"` // http/1.1, h2, h3
	Status          int64             `json:"status"`
	MimeType        string            `json:"mime_type"`
	ContentEncoding string            `json:"content_encoding,omitempty"`
	CacheControl    string            `json:"cache_control,omitempty"`
	EncodedBytes    int64             `json:"encoded_bytes"`
	Headers         map[string]string `json:"headers,omitempty"`
	Timing          *Timing           `json:"timing,omitempty"`
}

// Timing is the per-request waterfall breakdown in ms.
type Timing struct {
	DNS       float64 `json:"dns_ms,omitempty"`
	Connect   float64 `json:"connect_ms,omitempty"`
	SSL       float64 `json:"ssl_ms,omitempty"`
	Send      float64 `json:"send_ms,omitempty"`
	Wait      float64 `json:"wait_ms,omitempty"` // server TTFB for this request
	Receive   float64 `json:"receive_ms,omitempty"`
	Total     float64 `json:"total_ms,omitempty"`
	totalFrom float64 // monotonic requestTime, unexported
}

func nonneg(f float64) float64 {
	if f < 0 {
		return 0
	}
	return f
}

// Sample is the vitals data from a single page load.
type Sample struct {
	TTFBms    float64     `json:"ttfb_ms"`
	FCPms     *float64    `json:"fcp_ms,omitempty"`
	LCPms     *float64    `json:"lcp_ms,omitempty"`
	LCPElem   string      `json:"lcp_element,omitempty"`
	CLS       float64     `json:"cls"`
	TBTms     float64     `json:"tbt_ms"`
	DCLms     float64     `json:"dom_content_loaded_ms"`
	LoadMs    float64     `json:"load_ms"`
	JSHeap    int64       `json:"js_heap_bytes"`
	DOMNodes  int64       `json:"dom_nodes"`
	Responses []Response  `json:"responses"`
	Images    []ImageInfo `json:"images,omitempty"`
}

// Result is a complete measurement: samples + the Chrome version used.
type Result struct {
	URL             string            `json:"url"`
	BrowserVersion  string            `json:"browser_version"`
	HTTP            *httpcheck.Result `json:"http_checks,omitempty"`
	HTTP3           *http3.Result     `json:"http3,omitempty"`
	Attribution     *deep.Attribution `json:"attribution,omitempty"`
	Lighthouse      *deep.LHReport    `json:"lighthouse,omitempty"`
	PSI             *psi.Result       `json:"psi,omitempty"`
	Findings        []Finding         `json:"findings,omitempty"`
	Filmstrip       []string          `json:"filmstrip,omitempty"`
	FilmstripFormat string            `json:"filmstrip_format,omitempty"`
	Samples         []Sample          `json:"samples"`
}

// pageMetrics mirrors the JSON returned by collectScript.
type pageMetrics struct {
	TTFB    *float64 `json:"ttfb_ms"`
	DCL     *float64 `json:"dom_content_loaded_ms"`
	Load    *float64 `json:"load_ms"`
	FCP     *float64 `json:"fcp_ms"`
	LCP     *float64 `json:"lcp_ms"`
	LCPElem string   `json:"lcp_element"`
	CLS     float64  `json:"cls"`
	TBT     float64  `json:"tbt_ms"`
}

// injectScript runs at document start: paint/lcp/cls/longtask entries are
// observer-only in Chrome — getEntriesByType never returns them — so a
// buffered PerformanceObserver must be installed before navigation.
const injectScript = `(() => {
  window.__wvf = {fcp: null, lcp: null, lcpEl: null, cls: 0, tbt: 0};
  const on = (type, fn) => {
    try {
      new PerformanceObserver(list => {
        for (const e of list.getEntries()) fn(e);
      }).observe({type, buffered: true});
    } catch (_) {}
  };
  on('paint', e => {
    if (e.name === 'first-contentful-paint') window.__wvf.fcp = e.startTime;
  });
  on('largest-contentful-paint', e => {
    window.__wvf.lcp = e.startTime;
    window.__wvf.lcpEl = e.element
      ? e.element.tagName + (e.element.id ? '#' + e.element.id : '')
      : null;
  });
  on('layout-shift', e => {
    if (!e.hadRecentInput) window.__wvf.cls += e.value;
  });
  on('longtask', e => { window.__wvf.tbt += Math.max(0, e.duration - 50); });
})()`

// collectScript reads the observer buffer plus navigation timing post-load.
const imagesScript = `([...document.images].map(i => ({
  src: i.currentSrc || i.src || '',
  natural_width: i.naturalWidth,
  natural_height: i.naturalHeight,
  display_width: i.clientWidth,
  display_height: i.clientHeight,
  loading: i.loading
})))`

const collectScript = `(() => {
  const nav = performance.getEntriesByType('navigation')[0] || {};
  const w = window.__wvf || {};
  return {
    ttfb_ms: nav.responseStart ?? null,
    dom_content_loaded_ms: nav.domContentLoadedEventEnd ?? null,
    load_ms: nav.loadEventEnd ?? null,
    fcp_ms: w.fcp ?? null,
    lcp_ms: w.lcp ?? null,
    lcp_element: w.lcpEl ?? null,
    cls: w.cls ?? 0,
    tbt_ms: w.tbt ?? 0
  };
})()`

// Run measures url samples times in browserCtx and returns all samples.
func Run(ctx context.Context, url string, samples int, settle time.Duration) (*Result, error) {
	res := &Result{URL: url}
	for i := 0; i < samples; i++ {
		s, err := loadOnce(ctx, url, settle)
		if err != nil {
			return nil, fmt.Errorf("sample %d: %w", i+1, err)
		}
		res.Samples = append(res.Samples, *s)
	}
	ver, err := chromedp.Run(ctx, chromedp.Evaluate[string](`navigator.userAgent`))
	if err == nil {
		res.BrowserVersion = ver
	}
	return res, nil
}

func loadOnce(ctx context.Context, url string, settle time.Duration) (*Sample, error) {
	tabCtx, cancel := chromedp.NewContext(ctx)
	defer cancel()

	var responses []Response
	pending := map[string]int{}
	if err := chromedp.Listen(tabCtx, func(ev any) {
		switch e := ev.(type) {
		case *network.EventRequestWillBeSent:
			responses = append(responses, Response{URL: e.Request.URL})
			pending[e.RequestID.String()] = len(responses) - 1
		case *network.EventResponseReceived:
			i, ok := pending[e.RequestID.String()]
			if !ok {
				return
			}
			r := &responses[i]
			r.URL = e.Response.URL
			r.Protocol = e.Response.Protocol
			r.Status = e.Response.Status
			r.MimeType = e.Response.MimeType
			r.ContentEncoding = header(e.Response.Headers, "content-encoding")
			r.CacheControl = header(e.Response.Headers, "cache-control")
			r.Headers = headers(e.Response.Headers)
			r.EncodedBytes = int64(e.Response.EncodedDataLength)
			if t := e.Response.Timing; t != nil {
				rt := t.RequestTime * 1000 // monotonic ms
				r.Timing = &Timing{
					DNS:     nonneg(t.DNSEnd - t.DNSStart),
					Connect: nonneg(t.ConnectEnd - t.ConnectStart),
					SSL:     nonneg(t.SslEnd - t.SslStart),
					Send:    nonneg(t.SendEnd - t.SendStart),
					Wait:    nonneg(t.ReceiveHeadersEnd - t.SendEnd),
				}
				r.Timing.totalFrom = rt
			}
		case *network.EventLoadingFinished:
			i, ok := pending[e.RequestID.String()]
			if !ok {
				return
			}
			r := &responses[i]
			if r.EncodedBytes == 0 {
				r.EncodedBytes = int64(e.EncodedDataLength)
			}
			if r.Timing != nil {
				fin := float64(e.Timestamp) * 1000
				r.Timing.Total = float64(fin) - r.Timing.totalFrom
				r.Timing.Receive = nonneg(r.Timing.Total - (r.Timing.DNS + r.Timing.Connect + r.Timing.SSL + r.Timing.Send + r.Timing.Wait))
			}
		}
	}); err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	var pm pageMetrics
	var pmResult performance.GetMetricsResult
	err := chromedp.Do(tabCtx,
		chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			if _, err := cdp.Call(ctx, t, network.Enable, network.EnableParams{}); err != nil {
				return err
			}
			if _, err := cdp.Call(ctx, t, performance.Enable, performance.EnableParams{}); err != nil {
				return err
			}
			// Cold cache per sample: later samples must not ride earlier
			// ones' disk cache or request counts/protocols deflate.
			if _, err := cdp.Call(ctx, t, network.SetCacheDisabled,
				network.SetCacheDisabledParams{CacheDisabled: true}); err != nil {
				return err
			}
			if _, err := cdp.Call(ctx, t, page.AddScriptToEvaluateOnNewDocument,
				page.AddScriptToEvaluateOnNewDocumentParams{Source: injectScript}); err != nil {
				return err
			}
			return nil
		}),
		chromedp.Navigate(url),
		chromedp.Sleep(settle),
	)
	if err != nil {
		return nil, err
	}
	if pm, err = chromedp.Run(tabCtx, chromedp.Evaluate[pageMetrics](collectScript)); err != nil {
		return nil, fmt.Errorf("collect metrics: %w", err)
	}
	if pmResult, err = chromedp.Run(tabCtx, func(ctx context.Context, t *chromedp.Target) (performance.GetMetricsResult, error) {
		return cdp.Call(ctx, t, performance.GetMetrics, cdp.Empty{})
	}); err != nil {
		return nil, fmt.Errorf("performance metrics: %w", err)
	}
	images, _ := chromedp.Run(tabCtx, chromedp.Evaluate[[]ImageInfo](imagesScript))
	byURL := map[string]*Response{}
	for i := range responses {
		byURL[responses[i].URL] = &responses[i]
	}
	for i := range images {
		if r, ok := byURL[images[i].Src]; ok {
			images[i].Bytes = r.EncodedBytes
			images[i].Format = r.MimeType
		}
	}

	s := &Sample{
		CLS:       pm.CLS,
		TBTms:     pm.TBT,
		FCPms:     pm.FCP,
		LCPms:     pm.LCP,
		LCPElem:   pm.LCPElem,
		Responses: responses,
		Images:    images,
	}
	if pm.TTFB != nil {
		s.TTFBms = *pm.TTFB
	}
	if pm.DCL != nil {
		s.DCLms = *pm.DCL
	}
	if pm.Load != nil {
		s.LoadMs = *pm.Load
	}
	for _, m := range pmResult.Metrics {
		switch m.Name {
		case "JSHeapUsedSize":
			s.JSHeap = int64(m.Value)
		case "Nodes":
			s.DOMNodes = int64(m.Value)
		}
	}
	return s, nil
}

func headers(h network.Headers) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if str, ok := v.(string); ok {
			out[strings.ToLower(k)] = str
		}
	}
	return out
}

func header(h network.Headers, name string) string {
	for k, v := range h {
		if strings.EqualFold(k, name) {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}
