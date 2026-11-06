// Package store persists sites, runs, and jobs in SQLite.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Run is one stored measurement.
type Run struct {
	ID             int64     `json:"id"`
	SiteID         int64     `json:"site_id"`
	Engine         string    `json:"engine"`
	Trigger        string    `json:"trigger"`
	Status         string    `json:"status"`
	Error          string    `json:"error,omitempty"`
	StartedAt      time.Time `json:"started_at"`
	FinishedAt     time.Time `json:"finished_at"`
	BrowserVersion string    `json:"browser_version"`
	TTFBms         float64   `json:"ttfb_ms"`
	FCPms          *float64  `json:"fcp_ms,omitempty"`
	LCPms          *float64  `json:"lcp_ms,omitempty"`
	CLS            float64   `json:"cls"`
	TBTms          float64   `json:"tbt_ms"`
	RequestCount   int       `json:"request_count"`
	PageBytes      int64     `json:"page_bytes"`
	HTTPProtocol   string    `json:"http_protocol"`
	DetailJSON     string    `json:"detail_json"`
}

// InsertRun stores a run and applies the site's retention rules in the
// SAME transaction — pruning can never drift or race the insert.
func (s *Store) InsertRun(ctx context.Context, r *Run) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var fcp, lcp any
	if r.FCPms != nil {
		fcp = *r.FCPms
	}
	if r.LCPms != nil {
		lcp = *r.LCPms
	}
	res, err := tx.ExecContext(ctx,
		`INSERT INTO runs (site_id, engine, trigger, status, error, started_at,
		  finished_at, browser_version, ttfb_ms, fcp_ms, lcp_ms, cls, tbt_ms,
		  request_count, page_bytes, http_protocol, detail_json)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		r.SiteID, r.Engine, r.Trigger, r.Status, r.Error,
		r.StartedAt.UTC().Format(time.RFC3339),
		r.FinishedAt.UTC().Format(time.RFC3339),
		r.BrowserVersion, r.TTFBms, fcp, lcp, r.CLS, r.TBTms,
		r.RequestCount, r.PageBytes, r.HTTPProtocol, r.DetailJSON)
	if err != nil {
		return 0, fmt.Errorf("insert run: %w", err)
	}
	id, _ := res.LastInsertId()

	var keepLast, maxAge int
	if err := tx.QueryRowContext(ctx,
		`SELECT keep_last, max_age_days FROM sites WHERE id=?`, r.SiteID).
		Scan(&keepLast, &maxAge); err != nil {
		return 0, fmt.Errorf("read retention: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM runs WHERE site_id=? AND id NOT IN (
		   SELECT id FROM runs WHERE site_id=? ORDER BY started_at DESC, id DESC LIMIT ?)`,
		r.SiteID, r.SiteID, keepLast); err != nil {
		return 0, fmt.Errorf("prune keep_last: %w", err)
	}
	if maxAge > 0 {
		cutoff := time.Now().UTC().AddDate(0, 0, -maxAge).Format(time.RFC3339)
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM runs WHERE site_id=? AND started_at < ?`,
			r.SiteID, cutoff); err != nil {
			return 0, fmt.Errorf("prune max_age: %w", err)
		}
	}
	return id, tx.Commit()
}

// RunByID fetches one run.
func (s *Store) RunByID(ctx context.Context, id int64) (*Run, error) {
	r := &Run{}
	var fcp, lcp sql.NullFloat64
	var st, fi string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, site_id, engine, trigger, status, error, started_at, finished_at,
		  browser_version, ttfb_ms, fcp_ms, lcp_ms, cls, tbt_ms,
		  request_count, page_bytes, http_protocol, detail_json FROM runs WHERE id=?`, id).
		Scan(&r.ID, &r.SiteID, &r.Engine, &r.Trigger, &r.Status, &r.Error,
			&st, &fi, &r.BrowserVersion, &r.TTFBms,
			&fcp, &lcp, &r.CLS, &r.TBTms, &r.RequestCount, &r.PageBytes,
			&r.HTTPProtocol, &r.DetailJSON)
	if err != nil {
		return nil, fmt.Errorf("run %d: %w", id, err)
	}
	if fcp.Valid {
		r.FCPms = &fcp.Float64
	}
	if lcp.Valid {
		r.LCPms = &lcp.Float64
	}
	r.StartedAt, _ = time.Parse(time.RFC3339, st)
	r.FinishedAt, _ = time.Parse(time.RFC3339, fi)
	return r, nil
}

// RunsForSite lists runs for a site, newest first.
func (s *Store) RunsForSite(ctx context.Context, siteID int64, limit int) ([]Run, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, site_id, engine, trigger, status, error, started_at, finished_at,
		  browser_version, ttfb_ms, fcp_ms, lcp_ms, cls, tbt_ms,
		  request_count, page_bytes, http_protocol, detail_json
		 FROM runs WHERE site_id=? ORDER BY started_at DESC, id DESC LIMIT ?`,
		siteID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Run
	for rows.Next() {
		var r Run
		var fcp, lcp sql.NullFloat64
		var st, fi string
		if err := rows.Scan(&r.ID, &r.SiteID, &r.Engine, &r.Trigger, &r.Status,
			&r.Error, &st, &fi, &r.BrowserVersion, &r.TTFBms,
			&fcp, &lcp, &r.CLS, &r.TBTms, &r.RequestCount, &r.PageBytes,
			&r.HTTPProtocol, &r.DetailJSON); err != nil {
			return nil, err
		}
		r.StartedAt, _ = time.Parse(time.RFC3339, st)
		r.FinishedAt, _ = time.Parse(time.RFC3339, fi)
		if fcp.Valid {
			r.FCPms = &fcp.Float64
		}
		if lcp.Valid {
			r.LCPms = &lcp.Float64
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PruneSiteNow applies a site's retention rules outside an insert —
// used by `wvf prune` and the daily sweep.
func (s *Store) PruneSiteNow(ctx context.Context, siteID int64) (int64, error) {
	var keepLast, maxAge int
	if err := s.db.QueryRowContext(ctx,
		`SELECT keep_last, max_age_days FROM sites WHERE id=?`, siteID).
		Scan(&keepLast, &maxAge); err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var dropped int64
	res, err := tx.ExecContext(ctx,
		`DELETE FROM runs WHERE site_id=? AND id NOT IN (
		   SELECT id FROM runs WHERE site_id=? ORDER BY started_at DESC, id DESC LIMIT ?)`,
		siteID, siteID, keepLast)
	if err != nil {
		return 0, err
	}
	dropped, _ = res.RowsAffected()
	if maxAge > 0 {
		cutoff := time.Now().UTC().AddDate(0, 0, -maxAge).Format(time.RFC3339)
		res, err = tx.ExecContext(ctx,
			`DELETE FROM runs WHERE site_id=? AND started_at < ?`, siteID, cutoff)
		if err != nil {
			return 0, err
		}
		dropped += must(res)
	}
	return dropped, tx.Commit()
}

func must(res sql.Result) int64 { n, _ := res.RowsAffected(); return n }

// PrettyDetail returns the pretty-printed detail payload of a run.
func PrettyDetail(r *Run) (string, error) {
	var v any
	if err := json.Unmarshal([]byte(r.DetailJSON), &v); err != nil {
		return r.DetailJSON, nil
	}
	b, err := json.MarshalIndent(v, "", "  ")
	return string(b), err
}
