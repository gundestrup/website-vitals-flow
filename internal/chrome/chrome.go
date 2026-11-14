// Package chrome locates and launches a headless Chrome for measurement.
package chrome

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/remote"
)

// Candidates searched when no explicit path is configured. headless-shell
// first: smaller, purpose-built for this. macOS + Linux + PATH fallbacks.
var candidates = []string{
	"/Applications/Google Chrome for Testing.app/Contents/MacOS/Google Chrome for Testing",
	"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
	"/Applications/Chromium.app/Contents/MacOS/Chromium",
	"/usr/bin/chromium",
	"/usr/bin/chromium-browser",
	"/usr/bin/google-chrome",
}

const headlessShell = "chrome-headless-shell"

var pathNames = []string{
	headlessShell, "headless-shell", "chromium",
	"chromium-browser", "google-chrome", "chrome",
}

// Find locates a usable Chrome binary: explicit path → managed cache →
// well-known locations → PATH. Returns version alongside the path.
func Find(explicit string) (path, version string, err error) {
	if explicit != "" {
		if _, statErr := os.Stat(explicit); statErr != nil {
			return "", "", fmt.Errorf("configured browser_path %s: %w", explicit, statErr)
		}
		return explicit, probeVersion(explicit), nil
	}
	if home, herr := os.UserHomeDir(); herr == nil {
		managed := filepath.Join(home, ".cache", "wvf", headlessShell, headlessShell)
		if _, statErr := os.Stat(managed); statErr == nil {
			if v := probeVersion(managed); v != "unknown" {
				return managed, v, nil
			}
		}
	}
	for _, c := range candidates {
		if _, statErr := os.Stat(c); statErr == nil {
			// skip stubs that exist but can't run (e.g. snap placeholders)
			if v := probeVersion(c); v != "unknown" {
				return c, v, nil
			}
		}
	}
	for _, name := range pathNames {
		if p, lookErr := exec.LookPath(name); lookErr == nil {
			if v := probeVersion(p); v != "unknown" {
				return p, v, nil
			}
		}
	}
	return "", "", fmt.Errorf("no chrome/chromium found (checked %d paths + PATH); set browser_path or WVF_BROWSER_PATH", len(candidates))
}

func probeVersion(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 5_000_000_000)
	defer cancel()
	// #nosec G204 -- path is an operator-set browser_path, a managed-cache
	// binary, a well-known candidate, or a LookPath result; argv exec, no shell.
	out, err := exec.CommandContext(ctx, path, "--version").Output() // nosemgrep -- argv exec, no shell
	if err != nil {
		return "unknown"
	}
	v := string(out)
	for len(v) > 0 && (v[len(v)-1] == '\n' || v[len(v)-1] == ' ') {
		v = v[:len(v)-1]
	}
	return v
}

// Launch starts headless Chrome and returns a cancellable chromedp context
// (warmed via a trivial action). Caller must cancel to kill the browser.
func Launch(ctx context.Context, path string) (context.Context, context.CancelFunc, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, nil, fmt.Errorf("chrome binary %s: %w", path, err)
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		remote.WebSocket,
		chromedp.ExecPath(path),
		chromedp.Flag("headless", "new"),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("mute-audio", true),
		chromedp.Flag("disable-extensions", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("remote-debugging-port", "0"),
		chromedp.WindowSize(1366, 768),
	)
	// CI/container environments often lack the kernel features Chrome's
	// sandbox needs (user namespaces) or run as root; scoped to $CI so
	// local launches keep the sandbox.
	if os.Getenv("CI") != "" {
		opts = append(opts, chromedp.NoSandbox)
	}
	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)
	browserCtx, cancel := chromedp.NewContext(allocCtx)
	if _, err := chromedp.Run(browserCtx, chromedp.Evaluate[string](`navigator.userAgent`)); err != nil {
		cancel()
		allocCancel()
		return nil, nil, fmt.Errorf("launch chrome: %w", err)
	}
	return browserCtx, func() { cancel(); allocCancel() }, nil
}
