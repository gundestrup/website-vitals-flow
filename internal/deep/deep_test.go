package deep

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// fakeBin writes an executable script printing payload to stdout.
// Lighthouse/AvifEncode exec the binary by path — a shell script is a
// faithful stand-in for the real CLI contract.
func fakeBin(t *testing.T, name, script string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	// #nosec G306 -- test fixture must be executable to stand in for a binary
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

const lhrJSON = `{
	"categories": {"performance": {"score": 0.42}},
	"audits": {
		"render-blocking-resources": {
			"title": "Eliminate render-blocking resources",
			"description": "Resources block [learn more](https://web.dev/render-blocking).",
			"score": 0.2,
			"displayValue": "1.2 s",
			"details": {
				"summary": {"wastedMs": 1200, "wastedBytes": 51200},
				"items": [
					{"url": "http://x/app.css", "wastedBytes": 40960},
					{"node": {"selector": "script#main"}},
					{"wastedMs": 50}
				]
			}
		},
		"passing-audit": {"title": "ok", "score": 0.95},
		"nil-score-audit": {"title": "n/a"},
		"keyed-items-audit": {
			"title": "keyed", "score": 0.4,
			"details": {"summary": {}, "items": {"0": {"url": "http://x/k.js"}}}
		}
	}
}`

func TestLighthouse(t *testing.T) {
	bin := fakeBin(t, "lighthouse", "#!/bin/sh\nprintf '%s' '"+lhrJSON+"'\n")
	r, err := Lighthouse(context.Background(), bin, "http://x.test")
	if err != nil {
		t.Fatal(err)
	}
	if r.Scores["performance"] != 42 {
		t.Fatalf("scores: %v", r.Scores)
	}
	// score>=0.9 and nil-score audits are dropped → 2 findings
	if len(r.Findings) != 2 {
		t.Fatalf("want 2 findings, got %v", r.Findings)
	}
	byID := func(id string) *LHFinding {
		for i := range r.Findings {
			if r.Findings[i].ID == id {
				return &r.Findings[i]
			}
		}
		return nil
	}
	f := byID("render-blocking-resources")
	if f == nil || f.Score != 0.2 {
		t.Fatalf("finding: %+v", f)
	}
	if f.Link != "https://web.dev/render-blocking" {
		t.Fatalf("mdLink extraction: %q", f.Link)
	}
	if f.Savings != 1200 || f.SavingsB != 51200 {
		t.Fatalf("savings: %+v", f)
	}
	if len(f.Items) != 2 ||
		!strings.Contains(f.Items[0], "app.css") ||
		!strings.Contains(f.Items[0], "~40KB") ||
		f.Items[1] != "script#main" {
		t.Fatalf("items: %v", f.Items)
	}
	if k := byID("keyed-items-audit"); k == nil ||
		len(k.Items) != 1 || k.Items[0] != "http://x/k.js" {
		t.Fatalf("keyed items object should flatten: %+v", k)
	}
}

func TestLighthouseFailures(t *testing.T) {
	garbage := fakeBin(t, "lh-garbage", "#!/bin/sh\necho 'not json'\n")
	if _, err := Lighthouse(context.Background(), garbage, "http://x"); err == nil {
		t.Fatal("bad JSON should error")
	}
	failing := fakeBin(t, "lh-fail", "#!/bin/sh\nexit 1\n")
	if _, err := Lighthouse(context.Background(), failing, "http://x"); err == nil {
		t.Fatal("nonzero exit should error")
	}
}

func TestLHRHelpers(t *testing.T) {
	// cap at n
	items := lhItems(json.RawMessage(`[
		{"url": "http://a"}, {"url": "http://b"}, {"url": "http://c"}
	]`), 2)
	if len(items) != 2 {
		t.Fatalf("cap: %v", items)
	}
	// items that carry neither url nor selector are skipped
	if got := lhItems(json.RawMessage(`[{"wastedMs": 9}]`), 5); len(got) != 0 {
		t.Fatalf("empty item: %v", got)
	}
	// unparseable items → nil
	if lhItemList(json.RawMessage(`42`)) != nil {
		t.Fatal("non-list non-object items → nil")
	}
	// wastedMs branch
	if got := lhItemText(map[string]any{"url": "u", "wastedMs": 45.0}); got != "u (~45ms)" {
		t.Fatalf("wastedMs: %q", got)
	}
	// long URL truncation keeps the tail (… is 3 bytes → len 71, 69 runes)
	long := "http://x/" + strings.Repeat("a", 80)
	got := shortURL(long)
	if utf8.RuneCountInString(got) != 69 || !strings.HasPrefix(got, "…") {
		t.Fatalf("shortURL: %q", got)
	}
	if mdLink("see [x](https://web.dev/a) and [y](https://z)") != "https://web.dev/a" {
		t.Fatal("first link wins")
	}
	if mdLink("no links") != "" {
		t.Fatal("no link → empty")
	}
}

func TestAvifEncode(t *testing.T) {
	// avifenc argv: --speed 8 --min 35 --max 45 SRC DST — fake copies src→dst.
	bin := fakeBin(t, "avifenc", "#!/bin/sh\ncp \"$7\" \"$8\"\n")
	out, err := AvifEncode(context.Background(), bin, []byte("jpeg-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "jpeg-bytes" {
		t.Fatalf("transcode passthrough: %q", out)
	}
	bad := fakeBin(t, "avifenc-fail", "#!/bin/sh\nexit 2\n")
	if _, err := AvifEncode(context.Background(), bad, []byte("x")); err == nil {
		t.Fatal("encoder failure should error")
	}
}

func TestFindBins(t *testing.T) {
	// Presence of lighthouse/avifenc is environment-dependent — the
	// contract is only: found → existing path, else error. Never panic.
	if p, err := FindBin(); err == nil {
		if _, serr := os.Stat(p); serr != nil {
			t.Fatalf("FindBin returned missing path %q", p)
		}
	}
	if p, err := FindAvifenc(); err == nil {
		if _, serr := os.Stat(p); serr != nil {
			t.Fatalf("FindAvifenc returned missing path %q", p)
		}
	}
}
