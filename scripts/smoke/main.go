// smoke loads the dashboard headless and fails on console errors —
// catches alpine binding errors / JS exceptions that node --check
// cannot see (runtime-only failures). With -lcov it also collects
// V8 precise coverage for first-party JS and emits LCOV for Sonar.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/profiler"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/gundestrup/website-vitals-flow/internal/chrome"
)

// staticDir holds first-party JS eligible for LCOV emission — the
// script URL's basename must resolve to a file here.
const staticDir = "internal/server/static"

func main() {
	lcov := flag.String("lcov", "", "write JS coverage (LCOV) to this file")
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: smoke [-lcov file] <base-url>")
		os.Exit(2)
	}
	url := strings.TrimSuffix(flag.Arg(0), "/")
	path, _, err := chrome.Find(os.Getenv("WVF_BROWSER_PATH"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	bctx, cancel, err := chrome.Launch(context.Background(), path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer cancel()
	tabCtx, _ := chromedp.NewContext(bctx)
	var out string
	rbv := true
	eval := func(expr string) chromedp.Action[chromedp.Void] {
		return chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			r, err := cdp.Call(ctx, t, runtime.Evaluate,
				runtime.EvaluateParams{Expression: expr, ReturnByValue: &rbv})
			if err == nil && r.Result != nil {
				out = fmt.Sprintf("%v", r.Result.Value)
			}
			return err
		})
	}
	// collector must survive navigation — inject into every new document
	if err = chromedp.Do(tabCtx, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
		_, err := cdp.Call(ctx, t, page.AddScriptToEvaluateOnNewDocument,
			page.AddScriptToEvaluateOnNewDocumentParams{Source: `window.__e=[];window.addEventListener('error',e=>__e.push(e.message));
				(function(){const o=console.error;console.error=(...a)=>{__e.push(a.join(' '));return o.apply(console,a)}})()`})
		return err
	})); err != nil {
		fmt.Fprintln(os.Stderr, "smoke:", err)
		os.Exit(1)
	}
	var cov profiler.TakePreciseCoverageResult
	if *lcov != "" {
		tru := true
		if err = chromedp.Do(tabCtx, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			if _, err := cdp.Call(ctx, t, profiler.Enable, cdp.Empty{}); err != nil {
				return err
			}
			_, err := cdp.Call(ctx, t, profiler.StartPreciseCoverage,
				profiler.StartPreciseCoverageParams{CallCount: &tru, Detailed: &tru})
			return err
		})); err != nil {
			fmt.Fprintln(os.Stderr, "smoke: profiler:", err)
			os.Exit(1)
		}
	}
	err = chromedp.Do(tabCtx,
		chromedp.Navigate(url),
		chromedp.Sleep(5*time.Second),
		eval(`JSON.stringify(window.__e||[])`),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "smoke:", err)
		os.Exit(1)
	}
	if *lcov != "" {
		if err = chromedp.Do(tabCtx, chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			res, err := cdp.Call(ctx, t, profiler.TakePreciseCoverage, cdp.Empty{})
			cov = res
			return err
		})); err != nil {
			fmt.Fprintln(os.Stderr, "smoke: coverage:", err)
			os.Exit(1)
		}
		if err := writeLCOV(*lcov, cov.Result); err != nil {
			fmt.Fprintln(os.Stderr, "smoke: lcov:", err)
			os.Exit(1)
		}
	}
	txt := out
	var bad []string
	for _, kw := range []string{"Alpine", "Uncaught", "is not defined",
		"Cannot read propert", "TypeError", "ReferenceError"} {
		if strings.Contains(txt, kw) {
			bad = append(bad, kw)
		}
	}
	if len(bad) > 0 {
		fmt.Fprintln(os.Stderr, "smoke FAIL — console errors found:")
		fmt.Fprintln(os.Stderr, txt[:min(len(txt), 2000)])
		os.Exit(1)
	}
	fmt.Println("smoke ok: no console errors")
}

// writeLCOV converts V8 precise coverage into LCOV DA records for
// first-party static JS only. Narrowest ranges win (block coverage
// nests inside function ranges).
func writeLCOV(path string, scripts []*profiler.ScriptCoverage) error {
	var b strings.Builder
	for _, sc := range scripts {
		base := sc.URL[strings.LastIndex(sc.URL, "/")+1:]
		if !strings.HasSuffix(base, ".js") || strings.Contains(sc.URL, "/vendor/") {
			continue
		}
		srcPath := staticDir + "/" + base
		src, err := os.ReadFile(srcPath) // #nosec G304 -- fixed dir + own basename
		if err != nil {
			continue // vendored/extension script with no repo file
		}
		var rs []*profiler.CoverageRange
		for _, f := range sc.Functions {
			rs = append(rs, f.Ranges...)
		}
		// widest first — the narrowest block range wins per line, so
		// unexecuted inner blocks keep count 0.
		sort.Slice(rs, func(i, j int) bool {
			return rs[i].EndOffset-rs[i].StartOffset >
				rs[j].EndOffset-rs[j].StartOffset
		})
		hits := map[int]int64{}
		for _, r := range rs {
			for ln := lineOf(src, r.StartOffset); ln <= lineOf(src, r.EndOffset); ln++ {
				hits[ln] = r.Count
			}
		}
		if len(hits) == 0 {
			continue
		}
		lines := make([]int, 0, len(hits))
		lh := 0
		for ln, n := range hits {
			lines = append(lines, ln)
			if n > 0 {
				lh++
			}
		}
		sort.Ints(lines)
		fmt.Fprintf(&b, "TN:\nSF:%s\n", srcPath)
		for _, ln := range lines {
			fmt.Fprintf(&b, "DA:%d,%d\n", ln, hits[ln])
		}
		fmt.Fprintf(&b, "LF:%d\nLH:%d\nend_of_record\n", len(lines), lh)
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// lineOf maps a byte offset to a 1-based line number.
func lineOf(src []byte, off int64) int {
	if off > int64(len(src)) {
		off = int64(len(src))
	}
	return 1 + bytes.Count(src[:off], []byte{'\n'})
}
