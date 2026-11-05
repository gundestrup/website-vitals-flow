// Package deep holds the optional engine layers: web-vitals.js
// attribution and Lighthouse audits. Both degrade gracefully.
package deep

import (
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

//go:embed webvitals.js
var wvJS string

// Attribution is the web-vitals.js debugger payload.
type Attribution struct {
	Err         string   `json:"err,omitempty"`
	LCPElement  string   `json:"lcp_element,omitempty"`
	LCPURL      string   `json:"lcp_url,omitempty"`
	LCPSubparts []string `json:"lcp_subparts,omitempty"`
	CLSItems    []string `json:"cls_items,omitempty"`
	INPms       *float64 `json:"inp_ms,omitempty"`
	INPTarget   string   `json:"inp_target,omitempty"`
	INPType     string   `json:"inp_type,omitempty"`
}

const wvCollect = `(() => new Promise(done => {
  const out = {};
  if (typeof webVitals === 'undefined') { done({err: 'webVitals missing'}); return; }
  try {
    webVitals.onLCP(m => {
      const a = m.attribution || {};
      const le = a.lcpEntry || {};
      const el = le.element;
      out.lcp_element = el ? el.tagName + (el.id ? '#' + el.id : '') : '';
      if (a.url) out.lcp_url = a.url;
      out.lcp_subparts = [
        'ttfb=' + Math.round(a.timeToFirstByte || 0),
        'loadDelay=' + Math.round(a.resourceLoadDelay || 0),
        'loadTime=' + Math.round(a.resourceLoadDuration || 0),
        'renderDelay=' + Math.round(a.elementRenderDelay || 0)
      ];
    }, {reportAllChanges: true});
    webVitals.onCLS(m => {
      const a = m.attribution || {};
      const src = a.largestShiftSource;
      const node = src && src.node;
      out.cls_items = [
        'value=' + (a.largestShiftValue ?? m.value),
        'source=' + (node ? node.tagName + (node.id ? '#' + node.id : '') : '')
      ];
    }, {reportAllChanges: true});
    webVitals.onINP(m => {
      out.inp_ms = m.value;
      const a = m.attribution || {};
      if (a.interactionTarget) out.inp_target = String(a.interactionTarget).slice(0, 80);
      if (a.interactionType) out.inp_type = a.interactionType;
    }, {reportAllChanges: true});
    // web-vitals flushes reports on visibilitychange — synthesize it so
    // a settled headless page still emits LCP/CLS immediately.
    try { document.dispatchEvent(new Event('visibilitychange')); } catch (_) {}
  } catch (e) { out.err = String(e); }
  const t0 = performance.now();
  const tick = () => {
    if (out.lcp_element !== undefined || performance.now() - t0 > 3000) done(out);
    else setTimeout(tick, 100);
  };
  setTimeout(tick, 50);
}))()`

// CollectAttribution opens its own tab (sample tabs are closed after
// measure.Run), navigates, snaps filmstrip frames during settle,
// clicks several spots for worst-case lab-INP, injects the vendored
// attribution build, and returns the debugger payload + frames.
func CollectAttribution(ctx context.Context, url string) (*Attribution, []string, string, error) {
	tab, tcancel := chromedp.NewContext(ctx)
	defer tcancel()
	if err := chromedp.Do(tab, chromedp.Navigate(url)); err != nil {
		return nil, nil, "", err
	}
	var frames []string
	avifBin, _ := FindAvifenc()
	format := "webp"
	quality := int64(60)
	if avifBin != "" {
		format = "jpeg" // avifenc input must be jpg/png
		quality = int64(80)
	}
	for i := 0; i < 3; i++ { // filmstrip-lite: 3 frames across settle
		chromedp.Do(tab, chromedp.Sleep(500*time.Millisecond))
		f, q := format, quality
		buf, err := chromedp.Run(tab, func(ctx context.Context, t *chromedp.Target) ([]byte, error) {
			r, err := cdp.Call(ctx, t, page.CaptureScreenshot,
				page.CaptureScreenshotParams{Format: page.CaptureScreenshotFormat(f), Quality: &q})
			if err != nil {
				return nil, err
			}
			return r.Data, nil
		})
		if err != nil || len(buf) == 0 {
			continue
		}
		if avifBin != "" {
			if enc, err := AvifEncode(ctx, avifBin, buf); err == nil {
				buf = enc
			}
		}
		frames = append(frames, base64.StdEncoding.EncodeToString(buf))
	}
	// lab-INP: several trusted clicks so Event Timing reports the
	// WORST interaction — web-vitals attributes it.
	click(tab, 0.25, 0.30)
	click(tab, 0.50, 0.55)
	click(tab, 0.75, 0.70)
	if _, err := chromedp.Run(tab, chromedp.Evaluate[any](wvJS)); err != nil {
		return nil, nil, "", fmt.Errorf("inject web-vitals: %w", err)
	}
	res, err := chromedp.Run(tab,
		chromedp.Evaluate[Attribution](wvCollect, chromedp.EvalAwaitPromise))
	outFormat := "webp"
	if avifBin != "" && len(frames) > 0 {
		outFormat = "avif"
	}
	if err != nil {
		return nil, frames, outFormat, err
	}
	return &res, frames, outFormat, nil
}

// click dispatches a trusted left-click at viewport-relative x,y.
func click(tab context.Context, fx, fy float64) {
	chromedp.Run(tab, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		lay, err := cdp.Call(ctx, t, page.GetLayoutMetrics, cdp.Empty{})
		if err != nil {
			return err
		}
		x := lay.CSSVisualViewport.PageX + lay.CSSVisualViewport.ClientWidth*fx
		y := lay.CSSVisualViewport.PageY + lay.CSSVisualViewport.ClientHeight*fy
		for _, ev := range []input.DispatchMouseEventParams{
			{Type: input.DispatchMouseEventTypeMousePressed, X: x, Y: y, Button: input.MouseButtonLeft, ClickCount: 1},
			{Type: input.DispatchMouseEventTypeMouseReleased, X: x, Y: y, Button: input.MouseButtonLeft, ClickCount: 1},
		} {
			if _, err := cdp.Call(ctx, t, input.DispatchMouseEvent, ev); err != nil {
				return err
			}
		}
		return nil
	}))
}

// LHReport is the slice of a Lighthouse result we keep.
type LHReport struct {
	Scores   map[string]float64 `json:"scores"`
	Findings []LHFinding        `json:"findings"`
}

// LHFinding is one audit with a non-pass score.
type LHFinding struct {
	ID       string  `json:"id"`
	Title    string  `json:"title"`
	Score    float64 `json:"score"`
	Display  string  `json:"display,omitempty"`
	Link     string  `json:"link,omitempty"`
	Savings  float64 `json:"savings_ms,omitempty"`
	SavingsB float64 `json:"savings_bytes,omitempty"`
}

type lhJSON struct {
	Categories map[string]struct {
		Score *float64 `json:"score"`
	} `json:"categories"`
	Audits map[string]struct {
		Title   string   `json:"title"`
		Desc    string   `json:"description"`
		Score   *float64 `json:"score"`
		Display string   `json:"displayValue"`
		Details *struct {
			Summary struct {
				WastedMs    float64 `json:"wastedMs"`
				WastedBytes float64 `json:"wastedBytes"`
			} `json:"summary"`
		} `json:"details"`
	} `json:"audits"`
}

// Lighthouse runs the Node lighthouse CLI against url with provided
// (unthrottled) throttling for localhost parity. Returns nil error
// only when the binary exists and produces JSON.
func Lighthouse(ctx context.Context, bin, url string) (*LHReport, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, url,
		"--output=json", "--quiet", "--only-categories=performance",
		"--throttling-method=provided",
		"--chrome-flags=--headless=new --no-sandbox").Output()
	if err != nil {
		return nil, fmt.Errorf("lighthouse: %w", err)
	}
	var raw lhJSON
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse lighthouse json: %w", err)
	}
	r := &LHReport{Scores: map[string]float64{}}
	for k, c := range raw.Categories {
		if c.Score != nil {
			r.Scores[k] = *c.Score * 100
		}
	}
	for id, a := range raw.Audits {
		if a.Score == nil || *a.Score >= 0.9 {
			continue
		}
		f := LHFinding{ID: id, Title: a.Title, Score: *a.Score, Display: a.Display,
			Link: mdLink(a.Desc)}
		if a.Details != nil {
			f.Savings = a.Details.Summary.WastedMs
			f.SavingsB = a.Details.Summary.WastedBytes
		}
		r.Findings = append(r.Findings, f)
	}
	return r, nil
}

// mdLink pulls the first [text](url) link from Lighthouse audit
// descriptions — they carry the web.dev "Learn more" targets.
var mdLinkRe = regexp.MustCompile(`\]\((https?://[^)]+)\)`)

func mdLink(desc string) string {
	if m := mdLinkRe.FindStringSubmatch(desc); len(m) > 1 {
		return m[1]
	}
	return ""
}

// FindAvifenc locates an avifenc binary (optional filmstrip upgrade).
func FindAvifenc() (string, error) {
	return exec.LookPath("avifenc")
}

// AvifEncode transcodes jpeg/png bytes to AVIF via avifenc.
func AvifEncode(ctx context.Context, bin string, in []byte) ([]byte, error) {
	dir, err := os.MkdirTemp("", "wvf-avif")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "in.jpg")
	dst := filepath.Join(dir, "out.avif")
	if err := os.WriteFile(src, in, 0o644); err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, bin,
		"--speed", "8", "--min", "35", "--max", "45", src, dst).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("avifenc: %w (%s)", err, out)
	}
	return os.ReadFile(dst)
}

// FindBin locates a lighthouse executable.
func FindBin() (string, error) {
	for _, name := range []string{"lighthouse"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("lighthouse not on PATH")
}
