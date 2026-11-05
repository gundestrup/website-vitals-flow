// shot captures a page screenshot after a real wait.
// Usage: go run ./scripts/shot <url> <out.png> [wait-ms]
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"encoding/json"
	"github.com/chromedp/cdproto/cdp"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/chrome"
)

func ptr(b bool) *bool { return &b }

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: shot <url> <out.png> [wait-ms]")
		os.Exit(1)
	}
	wait := 3500 * time.Millisecond
	if len(os.Args) > 3 {
		if v, err := strconv.Atoi(os.Args[3]); err == nil {
			wait = time.Duration(v) * time.Millisecond
		}
	}
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
	var shot []byte
	err = chromedp.Do(tabCtx,
		chromedp.Navigate(os.Args[1]),
		chromedp.Sleep(wait),
		chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			if len(os.Args) < 5 {
				return nil
			}
			var out string
			r, err := cdp.Call(ctx, t, runtime.Evaluate,
				runtime.EvaluateParams{Expression: os.Args[4],
					ReturnByValue: ptr(true)})
			if err != nil {
				return err
			}
			b, _ := json.Marshal(r.Result.Value)
			out = string(b)
			fmt.Println("eval:", out)
			return nil
		}),
		chromedp.Func(func(ctx context.Context, t *chromedp.Target) error {
			r, err := cdp.Call(ctx, t, page.CaptureScreenshot,
				page.CaptureScreenshotParams{Format: "png"})
			if err != nil {
				return err
			}
			shot = r.Data
			return nil
		}),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(os.Args[2], shot, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("wrote", os.Args[2], len(shot), "bytes")
}
