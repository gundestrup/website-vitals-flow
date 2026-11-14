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
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestRetentionKeepLast(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	site, err := s.UpsertSite(ctx, SiteOpts{Key: "k", URL: "http://k", KeepLast: 2})
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
	id, _ := s.InsertJob(ctx, "scan", site.ID, false, "")
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
	if _, err := s.db.ExecContext(ctx, "PRAGMA user_version=99"); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, err := Open(ctx, dir+"/t.db"); err == nil {
		t.Fatal("newer schema should be refused")
	}
}

func TestRetentionMaxAge(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := Open(ctx, dir+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	site, err := s.UpsertSite(ctx, SiteOpts{
		Key: "age.test", URL: "http://age.test", KeepLast: 50, MaxAge: 7})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * 24 * time.Hour) // 30d > max_age 7d
	for _, st := range []time.Time{old, time.Now()} {
		if _, err := s.InsertRun(ctx, &Run{SiteID: site.ID, Engine: "cdp",
			Status: "ok", StartedAt: st, FinishedAt: st}); err != nil {
			t.Fatal(err)
		}
	}
	runs, _ := s.RunsForSite(ctx, site.ID, 10)
	if len(runs) != 1 {
		t.Fatalf("expired run should be pruned in-tx, got %d", len(runs))
	}
}

func TestStoreErrorPaths(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := Open(ctx, dir+"/t.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if _, err := s.SiteByKey(ctx, "ghost.test"); err == nil {
		t.Fatal("missing site should error")
	}
	if _, err := s.RunByID(ctx, 9999); err == nil {
		t.Fatal("missing run should error")
	}
	if _, _, err := s.JobByID(ctx, 9999); err == nil {
		t.Fatal("missing job should error")
	}
	if err := s.RemoveSite(ctx, "ghost.test"); err == nil {
		t.Fatal("removing a missing site should error")
	}
	if _, err := s.PruneSiteNow(ctx, 9999); err == nil {
		t.Fatal("pruning a missing site should error")
	}
	if js, err := s.RecentJobs(ctx, 5); err != nil || len(js) != 0 {
		t.Fatalf("empty jobs list: %v %v", js, err)
	}
}
