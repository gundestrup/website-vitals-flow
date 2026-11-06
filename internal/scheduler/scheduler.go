// Package scheduler enqueues due sites on an interval tick.
package scheduler

import (
	"context"
	"database/sql"
	"time"

	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/jobs"
	"forgejo.borg-collective.eu/svend/website-vitals-flow/internal/store"
)

// ParseInterval turns "30m"/"6h"/"daily" into a duration. Empty = never.
func ParseInterval(s string) (time.Duration, error) {
	if s == "" || s == "manual" {
		return 0, nil
	}
	if s == "daily" {
		return 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

// Run ticks every tick duration and enqueues overdue scheduled sites.
// Injectable tick keeps tests fast — same convention as KBM runHeartbeat.
func Run(ctx context.Context, st *store.Store, r *jobs.Runner, tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sweep(ctx, st, r)
		}
	}
}

func sweep(ctx context.Context, st *store.Store, r *jobs.Runner) {
	sites, err := st.ListSites(ctx)
	if err != nil {
		return
	}
	for _, site := range sites {
		interval, err := ParseInterval(site.Schedule)
		if err != nil || interval <= 0 {
			continue
		}
		last, err := st.LatestRunTime(ctx, site.ID)
		if err == sql.ErrNoRows || err != nil || time.Since(last) >= interval {
			_, _ = r.Enqueue(ctx, site.ID, "scan", site.Deep, "")
		}
	}
}
