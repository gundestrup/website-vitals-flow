// smoke loads the dashboard headless and fails on console errors —
// catches alpine binding errors / JS exceptions that node --check
// cannot see (runtime-only failures).
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/chrome"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: smoke <base-url>")
		os.Exit(2)
	}
	url := strings.TrimSuffix(os.Args[1], "/")
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
	err = chromedp.Do(tabCtx,
		chromedp.Navigate(url),
		chromedp.Sleep(5*time.Second),
		eval(`JSON.stringify(window.__e||[])`),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "smoke:", err)
		os.Exit(1)
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
