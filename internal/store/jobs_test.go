package store

import (
	"context"
	"testing"
)

func TestJobPageRoundTrip(t *testing.T) {
	st, err := Open(context.Background(), t.TempDir()+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	site, _ := st.GetOrCreateSite(context.Background(), "p.test", "http://p.test", 0, 0)
	id, err := st.InsertJob(context.Background(), "scan", site.ID, false, "", "maps/North")
	if err != nil {
		t.Fatal(err)
	}
	pg, err := st.JobPage(context.Background(), id)
	if err != nil || pg != "maps/North" {
		t.Fatalf("page: %q %v", pg, err)
	}
}

func TestJobLookupDetailSetRun(t *testing.T) {
	st, err := Open(context.Background(), t.TempDir()+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	site, err := st.GetOrCreateSite(ctx, "jl.test", "http://jl.test", 10, 7)
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.InsertJob(ctx, "scan", site.ID, true, "lcp", "p/1")
	if err != nil {
		t.Fatal(err)
	}
	var kind string
	var siteID int64
	if err := st.JobLookup(ctx, id, &kind, &siteID); err != nil {
		t.Fatal(err)
	}
	if kind != "scan" || siteID != site.ID {
		t.Fatalf("JobLookup: %q %d", kind, siteID)
	}
	if err := st.JobLookup(ctx, 99999, &kind, &siteID); err == nil {
		t.Error("missing job should error")
	}
	if err := st.JobDetail(ctx, id, "halfway"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetJobRun(ctx, id, 42); err != nil {
		t.Fatal(err)
	}
	j, runID, err := st.JobByID(ctx, id)
	if err != nil || runID != 42 || j.Detail != "halfway" {
		t.Fatalf("job: %v run=%d detail=%q", err, runID, j.Detail)
	}
	pg, err := st.JobPage(ctx, id)
	if err != nil || pg != "p/1" {
		t.Fatalf("JobPage: %q %v", pg, err)
	}
}
