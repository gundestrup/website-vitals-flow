package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/gundestrup/website-vitals-flow/internal/config"
	"github.com/gundestrup/website-vitals-flow/internal/jobs"
	"github.com/gundestrup/website-vitals-flow/internal/store"
)

func TestParseInterval(t *testing.T) {
	for _, c := range []struct {
		in   string
		want time.Duration
	}{
		{"", 0}, {"manual", 0}, {"daily", 24 * time.Hour},
		{"30m", 30 * time.Minute}, {"6h", 6 * time.Hour},
	} {
		got, err := ParseInterval(c.in)
		if err != nil || got != c.want {
			t.Errorf("%q → %v %v, want %v", c.in, got, err, c.want)
		}
	}
	if _, err := ParseInterval("bogus"); err == nil {
		t.Error("bogus interval should error")
	}
}

func tempStore(t *testing.T) (*store.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, t.TempDir()+"/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, ctx
}

func TestSweepEnqueuesDueSites(t *testing.T) {
	st, ctx := tempStore(t)
	r := jobs.New(st, config.Config{})
	// due: scheduled, never scanned
	due, err := st.UpsertSite(ctx, store.SiteOpts{
		Key: "due.test", URL: "http://due.test", Schedule: "1ns",
		KeepLast: 10, MaxAge: 56})
	if err != nil {
		t.Fatal(err)
	}
	// fresh: scheduled but scanned just now
	fresh, err := st.UpsertSite(ctx, store.SiteOpts{
		Key: "fresh.test", URL: "http://fresh.test", Schedule: "24h",
		KeepLast: 10, MaxAge: 56})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.InsertRun(ctx, &store.Run{SiteID: fresh.ID,
		Engine: "cdp", Status: "ok",
		StartedAt: time.Now(), FinishedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// manual: schedule empty → never due
	if _, err := st.UpsertSite(ctx, store.SiteOpts{
		Key: "man.test", URL: "http://man.test", Schedule: "",
		KeepLast: 10, MaxAge: 56}); err != nil {
		t.Fatal(err)
	}
	// bad interval: unparseable → skipped, not fatal
	if _, err := st.UpsertSite(ctx, store.SiteOpts{
		Key: "bad.test", URL: "http://bad.test", Schedule: "bogus",
		KeepLast: 10, MaxAge: 56}); err != nil {
		t.Fatal(err)
	}

	sweep(ctx, st, r)

	js, err := st.RecentJobs(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(js) != 1 {
		t.Fatalf("want exactly 1 enqueued job, got %d (%+v)", len(js), js)
	}
	if js[0].SiteID != due.ID {
		t.Fatalf("due site should be enqueued, got site %d", js[0].SiteID)
	}
}

func TestRunTicksAndStops(t *testing.T) {
	st, ctx := tempStore(t)
	r := jobs.New(st, config.Config{})
	if _, err := st.UpsertSite(ctx, store.SiteOpts{
		Key: "tick.test", URL: "http://tick.test", Schedule: "1ns",
		KeepLast: 10, MaxAge: 56}); err != nil {
		t.Fatal(err)
	}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go Run(rctx, st, r, time.Millisecond)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if js, _ := st.RecentJobs(ctx, 10); len(js) > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("ticker never enqueued the due site")
}
