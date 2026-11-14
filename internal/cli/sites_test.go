package cli

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gundestrup/website-vitals-flow/internal/store"
)

// chdirTemp mirrors compare_test.go: commands resolve state.db + wvf.yml
// against cwd, so each CLI test runs inside a scratch dir.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(old) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSitesAddListRemoveFlow(t *testing.T) {
	chdirTemp(t)
	ctx := context.Background()

	cmd := sitesCmd()
	cmd.SetArgs([]string{"add", "http://flow.test", "--label", "Flow",
		"--schedule", "30m", "--deep", "--only", "lcp,cls",
		"--keep-last", "5", "--max-age-days", "3"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sites add: %v", err)
	}
	st, err := store.Open(ctx, "state.db")
	if err != nil {
		t.Fatal(err)
	}
	site, err := st.SiteByKey(ctx, "flow.test")
	if err != nil {
		t.Fatal(err)
	}
	if site.Label != "Flow" || site.Schedule != "30m" || !site.Deep ||
		site.Only != "lcp,cls" || site.KeepLast != 5 || site.MaxAgeDays != 3 {
		t.Fatalf("site row: %+v", site)
	}
	_ = st.Close()

	// upsert: add same URL again with a new label
	cmd = sitesCmd()
	cmd.SetArgs([]string{"add", "http://flow.test", "--label", "Renamed"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sites add upsert: %v", err)
	}
	st, _ = store.Open(ctx, "state.db")
	site, _ = st.SiteByKey(ctx, "flow.test")
	if site.Label != "Renamed" {
		t.Fatalf("upsert label: %q", site.Label)
	}
	_ = st.Close()

	cmd = sitesCmd()
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sites list: %v", err)
	}

	cmd = sitesCmd()
	cmd.SetArgs([]string{"remove", "flow.test"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("sites remove: %v", err)
	}
	st, _ = store.Open(ctx, "state.db")
	if _, err := st.SiteByKey(ctx, "flow.test"); err == nil {
		t.Fatal("site should be gone")
	}
	_ = st.Close()
}

func TestPruneCommand(t *testing.T) {
	dir := chdirTemp(t)
	ctx := context.Background()
	st, err := store.Open(ctx, dir+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	site, err := st.GetOrCreateSite(ctx, "pr.test", "http://pr.test", 2, 56)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := st.InsertRun(ctx, &store.Run{SiteID: site.ID,
			Engine: "cdp", Status: "ok",
			StartedAt:  time.Now().Add(-time.Duration(i) * time.Hour),
			FinishedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	_ = st.Close()

	if err := pruneCmd().RunE(nil, nil); err != nil {
		t.Fatalf("prune: %v", err)
	}
	st, _ = store.Open(ctx, dir+"/state.db")
	runs, _ := st.RunsForSite(ctx, site.ID, 10)
	if len(runs) != 2 {
		t.Fatalf("keep_last=2 should leave 2 runs, got %d", len(runs))
	}
	_ = st.Close()
}

func TestReportCommand(t *testing.T) {
	dir := chdirTemp(t)
	ctx := context.Background()
	st, err := store.Open(ctx, dir+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	site, _ := st.GetOrCreateSite(ctx, "rp.test", "http://rp.test", 10, 56)
	runID, err := st.InsertRun(ctx, &store.Run{SiteID: site.ID,
		Engine: "cdp", Status: "ok", Trigger: "manual",
		StartedAt: time.Now(), FinishedAt: time.Now(),
		TTFBms: 100, CLS: 0.1, TBTms: 50,
		DetailJSON: `{"samples":[]}`})
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	cmd := reportCmd()
	if err := cmd.RunE(cmd, []string{fmt.Sprint(runID)}); err != nil {
		t.Fatalf("report: %v", err)
	}
	cmd = reportCmd()
	if err := cmd.Flags().Set("json", "true"); err != nil {
		t.Fatal(err)
	}
	if err := cmd.RunE(cmd, []string{fmt.Sprint(runID)}); err != nil {
		t.Fatalf("report --json: %v", err)
	}
	cmd = reportCmd()
	if err := cmd.RunE(cmd, []string{"not-a-number"}); err == nil {
		t.Fatal("bad run id should error")
	}
}

func TestJobsCommand(t *testing.T) {
	dir := chdirTemp(t)
	ctx := context.Background()
	st, err := store.Open(ctx, dir+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	site, _ := st.GetOrCreateSite(ctx, "jb.test", "http://jb.test", 10, 56)
	if _, err := st.InsertJob(ctx, "scan", site.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()

	cmd := jobsCmd()
	if err := cmd.RunE(cmd, nil); err != nil {
		t.Fatalf("jobs: %v", err)
	}
}
