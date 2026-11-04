package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// Site is one monitored (or one-shot-scanned) target.
type Site struct {
	ID         int64
	Key        string
	URL        string
	Label      string
	Schedule   string
	KeepLast   int
	MaxAgeDays int
	CreatedAt  time.Time
	Deep       bool
}

// GetOrCreateSite returns the site for key, creating it with the given
// retention defaults on first sight. Every scan lands under a site key —
// one-shot or monitored alike.
func (s *Store) GetOrCreateSite(ctx context.Context, key, url string, keepLast, maxAge int) (*Site, error) {
	if site, err := s.SiteByKey(ctx, key); err == nil {
		return site, nil
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO sites (key, url, keep_last, max_age_days, created_at)
		 VALUES (?,?,?,?,?) ON CONFLICT(key) DO NOTHING`,
		key, url, keepLast, maxAge, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("insert site %s: %w", key, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return s.SiteByKey(ctx, key)
	}
	return s.SiteByKey(ctx, key)
}

// SiteByKey fetches one site by key. sql.ErrNoRows → nil, nil.
func (s *Store) SiteByKey(ctx context.Context, key string) (*Site, error) {
	site := &Site{}
	var created string
	var deepInt int
	err := s.db.QueryRowContext(ctx,
		`SELECT id, key, url, label, schedule, keep_last, max_age_days, created_at, COALESCE(deep,0)
		 FROM sites WHERE key=?`, key).
		Scan(&site.ID, &site.Key, &site.URL, &site.Label, &site.Schedule,
			&site.KeepLast, &site.MaxAgeDays, &created, &deepInt)
	if err == sql.ErrNoRows {
		return nil, sql.ErrNoRows
	}
	if err != nil {
		return nil, fmt.Errorf("site %s: %w", key, err)
	}
	site.CreatedAt, _ = time.Parse(time.RFC3339, created)
	site.Deep = deepInt == 1
	return site, nil
}

// UpsertSite sets schedule/label/retention on an existing or new site.
func (s *Store) UpsertSite(ctx context.Context, key, url, label, schedule string, keepLast, maxAge int, deep bool) (*Site, error) {
	d := 0
	if deep {
		d = 1
	}
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO sites (key, url, label, schedule, keep_last, max_age_days, deep, created_at)
		 VALUES (?,?,?,?,?,?,?,?)
		 ON CONFLICT(key) DO UPDATE SET
		   url=excluded.url, label=excluded.label, schedule=excluded.schedule,
		   keep_last=excluded.keep_last, max_age_days=excluded.max_age_days,
		   deep=excluded.deep`,
		key, url, label, schedule, keepLast, maxAge, d,
		time.Now().UTC().Format(time.RFC3339)); err != nil {
		return nil, fmt.Errorf("upsert site %s: %w", key, err)
	}
	return s.SiteByKey(ctx, key)
}

// ListSites returns every site, newest first.
func (s *Store) ListSites(ctx context.Context) ([]Site, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, key, url, label, schedule, keep_last, max_age_days, created_at, COALESCE(deep,0)
		 FROM sites ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Site
	for rows.Next() {
		var site Site
		var created string
		var deepInt int
		if err := rows.Scan(&site.ID, &site.Key, &site.URL, &site.Label,
			&site.Schedule, &site.KeepLast, &site.MaxAgeDays, &created, &deepInt); err != nil {
			return nil, err
		}
		site.CreatedAt, _ = time.Parse(time.RFC3339, created)
		site.Deep = deepInt == 1
		out = append(out, site)
	}
	return out, rows.Err()
}

// RemoveSite deletes a site and (via cascade) its runs/jobs.
func (s *Store) RemoveSite(ctx context.Context, key string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sites WHERE key=?`, key)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no site %q", key)
	}
	return nil
}
