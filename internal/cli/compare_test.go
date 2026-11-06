package cli

import (
	"context"
	"os"
	"testing"
	"time"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/store"
)

// Compare runs end-to-end against a seeded store via the real command.
func TestCompareCommand(t *testing.T) {
	dir := t.TempDir()
	old, _ := os.Getwd()
	defer func() { _ = os.Chdir(old) }()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	st, err := store.Open(ctx, "state.db")
	if err != nil {
		t.Fatal(err)
	}
	site, err := st.GetOrCreateSite(ctx, "cmp.test", "http://cmp.test", 10, 7)
	if err != nil {
		t.Fatal(err)
	}
	for i, ttfb := range []float64{100, 150} {
		if _, err := st.InsertRun(ctx, &store.Run{SiteID: site.ID, Engine: "cdp",
			Status: "ok", StartedAt: time.Now().Add(-time.Duration(2-i) * time.Hour),
			FinishedAt: time.Now(), TTFBms: ttfb}); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.Close()

	cmd := compareCmd()
	if err := cmd.RunE(cmd, []string{"cmp.test"}); err != nil {
		t.Fatalf("compare failed: %v", err)
	}
	// one-run site must error
	st2, _ := store.Open(ctx, "state.db")
	s2, _ := st2.GetOrCreateSite(ctx, "one.test", "http://one.test", 10, 7)
	if _, err := st2.InsertRun(ctx, &store.Run{SiteID: s2.ID, Engine: "cdp", Status: "ok",
		StartedAt: time.Now(), FinishedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	_ = st2.Close()
	if err := cmd.RunE(cmd, []string{"one.test"}); err == nil {
		t.Fatal("expected error for single-run site")
	}
}
