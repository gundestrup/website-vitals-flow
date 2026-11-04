package store

import (
	"context"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), t.TempDir()+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRetentionKeepLast(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	site, err := s.UpsertSite(ctx, "k", "http://k", "", "", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		r := &Run{SiteID: site.ID, Engine: "cdp", Trigger: "cli", Status: "ok",
			StartedAt: time.Now().Add(-time.Duration(i) * time.Hour), FinishedAt: time.Now()}
		if _, err := s.InsertRun(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	runs, _ := s.RunsForSite(ctx, site.ID, 50)
	if len(runs) != 2 {
		t.Fatalf("keep_last=2 but %d runs", len(runs))
	}
	if runs[0].StartedAt.Before(runs[1].StartedAt) {
		t.Error("newest run not first")
	}
}

func TestGetOrCreateIdempotent(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	a, _ := s.GetOrCreateSite(ctx, "k", "http://k", 10, 7)
	b, _ := s.GetOrCreateSite(ctx, "k", "http://k", 10, 7)
	if a.ID != b.ID {
		t.Fatal("duplicate site for same key")
	}
}

func TestJobsLifecycle(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	site, _ := s.GetOrCreateSite(ctx, "k", "http://k", 10, 7)
	id, _ := s.InsertJob(ctx, "scan", site.ID)
	if j, _, _ := s.JobByID(ctx, id); j.State != "queued" {
		t.Fatal("job not queued")
	}
	n, _ := s.FailStaleJobs(ctx)
	if n != 1 {
		t.Fatalf("expected 1 stale job, %d", n)
	}
	j, _, _ := s.JobByID(ctx, id)
	if j.State != "failed" {
		t.Fatalf("state %q want failed", j.State)
	}
}

func TestNewerSchemaRejected(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := Open(ctx, dir+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	s.db.ExecContext(ctx, "PRAGMA user_version=99")
	s.Close()
	if _, err := Open(ctx, dir+"/t.db"); err == nil {
		t.Fatal("newer schema should be refused")
	}
}
