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

	"github.com/chromedp/chromedp"
)

//go:embed webvitals.js
var wvJS string

// Attribution is the web-vitals.js debugger payload.
type Attribution struct {
	LCPElement  string   `json:"lcp_element,omitempty"`
	LCPSubparts []string `json:"lcp_subparts,omitempty"`
	CLSItems    []string `json:"cls_items,omitempty"`
	INPms       *float64 `json:"inp_ms,omitempty"`
}

const wvCollect = `(() => new Promise(done => {
  const out = {};
  try {
    webVitals.onLCP(m => {
      if (m.attribution) {
        out.lcp_element = m.attribution.element || '';
        const a = m.attribution;
        out.lcp_subparts = [
          'ttfb=' + Math.round(a.timeToFirstByte || 0),
          'loadDelay=' + Math.round(a.resourceLoadDelay || 0),
          'loadTime=' + Math.round(a.resourceLoadTime || 0),
          'renderDelay=' + Math.round(a.elementRenderDelay || 0)
        ];
      }
    }, {reportAllChanges: true});
    webVitals.onCLS(m => {
      out.cls_items = (m.attribution?.largestShiftSource
        ? [m.attribution.largestShiftSource.node ? 'node' : 'shift',
           'value=' + (m.attribution.largestShiftValue ?? m.value)]
        : []);
    });
    webVitals.onINP(m => { out.inp_ms = m.value; });
  } catch (e) { out.err = String(e); }
  setTimeout(() => done(out), 800);
}))`

// CollectAttribution injects the vendored attribution build into a
// loaded page and returns the debugger payload (evaluates with await).
func CollectAttribution(ctx context.Context) (*Attribution, error) {
	if _, err := chromedp.Run(ctx, chromedp.Evaluate[any](wvJS)); err != nil {
		return nil, fmt.Errorf("inject web-vitals: %w", err)
	}
	res, err := chromedp.Run(ctx,
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
