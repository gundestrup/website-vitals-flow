package store

import (
	"context"
	"fmt"
	"time"
)

// Job is a queued/running/finished unit of work (scan).
type Job struct {
	ID         int64
	Kind       string
	SiteID     int64
	State      string
	Detail     string
	QueuedAt   string
	StartedAt  string
	FinishedAt string
	Deep       bool
}

// InsertJob enqueues a job row and returns its id.
func (s *Store) InsertJob(ctx context.Context, kind string, siteID int64, deep bool) (int64, error) {
	d := 0
	if deep {
		d = 1
	}
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO jobs (kind, site_id, state, queued_at, deep) VALUES (?,?, 'queued', ?, ?)`,
		kind, siteID, time.Now().UTC().Format(time.RFC3339), d)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// JobState moves a job to running/done/failed with detail text.
func (s *Store) JobState(ctx context.Context, id int64, state, detail string) error {
	col := "started_at"
	if state == "done" || state == "failed" {
		col = "finished_at"
	}
	_, err := s.db.ExecContext(ctx,
		fmt.Sprintf(`UPDATE jobs SET state=?, detail=?, %s=? WHERE id=?`, col),
		state, detail, time.Now().UTC().Format(time.RFC3339), id)
	return err
}

// FailStaleJobs marks leftover 'running' jobs failed — boot-time cleanup.
func (s *Store) FailStaleJobs(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET state='failed', detail='interrupted: wvf restarted',
		 finished_at=? WHERE state IN ('running','queued')`,
		time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// RecentJobs lists newest jobs first.
func (s *Store) RecentJobs(ctx context.Context, limit int) ([]Job, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, kind, site_id, state, detail, queued_at,
		 COALESCE(started_at,''), COALESCE(finished_at,'')
		 FROM jobs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.Kind, &j.SiteID, &j.State, &j.Detail,
			&j.QueuedAt, &j.StartedAt, &j.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// LatestRunTime returns the newest run's started_at for a site.
func (s *Store) LatestRunTime(ctx context.Context, siteID int64) (time.Time, error) {
	var v string
	err := s.db.QueryRowContext(ctx,
		`SELECT started_at FROM runs WHERE site_id=? ORDER BY started_at DESC, id DESC LIMIT 1`,
		siteID).Scan(&v)
	if err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, v)
}

// JobLookup returns kind and site for a job.
func (s *Store) JobLookup(ctx context.Context, id int64, kind *string, siteID *int64) error {
	return s.db.QueryRowContext(ctx,
		`SELECT kind, site_id FROM jobs WHERE id=?`, id).Scan(kind, siteID)
}

// SiteByID fetches a site by primary key.
func (s *Store) SiteByID(ctx context.Context, id int64) (*Site, error) {
	site := &Site{}
	var created string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, key, url, label, schedule, keep_last, max_age_days, created_at
		 FROM sites WHERE id=?`, id).
		Scan(&site.ID, &site.Key, &site.URL, &site.Label, &site.Schedule,
			&site.KeepLast, &site.MaxAgeDays, &created)
	if err != nil {
		return nil, err
	}
	site.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return site, nil
}

// JobDetail updates only the progress detail of a live job.
func (s *Store) JobDetail(ctx context.Context, id int64, detail string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET detail=? WHERE id=?`, detail, id)
	return err
}

// SetJobRun links a finished job to its produced run.
func (s *Store) SetJobRun(ctx context.Context, id, runID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET run_id=? WHERE id=?`, runID, id)
	return err
}

// JobByID fetches one job.
func (s *Store) JobByID(ctx context.Context, id int64) (*Job, int64, error) {
	var j Job
	var runID int64
	var st, fi string
	var deepInt int
	err := s.db.QueryRowContext(ctx,
		`SELECT id, kind, site_id, state, detail, queued_at,
		  COALESCE(started_at,''), COALESCE(finished_at,''), COALESCE(run_id,0),
		  COALESCE(deep,0)
		 FROM jobs WHERE id=?`, id).Scan(&j.ID, &j.Kind, &j.SiteID, &j.State,
		&j.Detail, &j.QueuedAt, &st, &fi, &runID, &deepInt)
	j.Deep = deepInt == 1
	j.StartedAt, j.FinishedAt = st, fi
	return &j, runID, err
}
