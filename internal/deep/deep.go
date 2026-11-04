// Package deep holds the optional engine layers: web-vitals.js
// attribution and Lighthouse audits. Both degrade gracefully.
package deep

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/input"
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
// measure.Run), navigates, injects the vendored attribution build, and
// returns the debugger payload.
func CollectAttribution(ctx context.Context, url string) (*Attribution, error) {
	tab, tcancel := chromedp.NewContext(ctx)
	defer tcancel()
	if err := chromedp.Do(tab, chromedp.Navigate(url),
		chromedp.Sleep(1500*time.Millisecond)); err != nil {
		return nil, err
	}
	// lab-INP: a trusted input event makes Event Timing emit real
	// interaction entries — web-vitals can then attribute the
	// interaction's target/type/next-paint delay.
	chromedp.Run(tab, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		for _, ev := range []input.DispatchMouseEventParams{
			{Type: input.DispatchMouseEventTypeMousePressed, X: 100, Y: 100, Button: input.MouseButtonLeft, ClickCount: 1},
			{Type: input.DispatchMouseEventTypeMouseReleased, X: 100, Y: 100, Button: input.MouseButtonLeft, ClickCount: 1},
		} {
			if _, err := cdp.Call(ctx, t, input.DispatchMouseEvent, ev); err != nil {
				return err
			}
		}
		return nil
	}))
	if _, err := chromedp.Run(tab, chromedp.Evaluate[any](wvJS)); err != nil {
		return nil, fmt.Errorf("inject web-vitals: %w", err)
	}
	res, err := chromedp.Run(tab,
		chromedp.Evaluate[Attribution](wvCollect, chromedp.EvalAwaitPromise))
	if err != nil {
		return nil, err
	}
	return &res, nil
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
	Savings  float64 `json:"savings_ms,omitempty"`
	SavingsB float64 `json:"savings_bytes,omitempty"`
}

type lhJSON struct {
	Categories map[string]struct {
		Score *float64 `json:"score"`
	} `json:"categories"`
	Audits map[string]struct {
		Title   string   `json:"title"`
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
		f := LHFinding{ID: id, Title: a.Title, Score: *a.Score, Display: a.Display}
		if a.Details != nil {
			f.Savings = a.Details.Summary.WastedMs
			f.SavingsB = a.Details.Summary.WastedBytes
		}
		r.Findings = append(r.Findings, f)
	}
	return r, nil
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
