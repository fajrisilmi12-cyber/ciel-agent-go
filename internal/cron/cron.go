package cron

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Job represents a scheduled task.
type Job struct {
	ID         string
	Name       string
	Schedule   string // simple 5-field cron: "* * * * *"
	Prompt     string
	ToolPolicy string // "*" or comma-separated names
	Enabled    bool
	NextRun    time.Time
	LastRun    *time.Time
	LastResult string
	CreatedAt  time.Time
}

// Executor runs an agent turn; provided by the wiring layer.
type Executor func(ctx context.Context, prompt, toolPolicy string) (string, error)

// Scheduler checks for due jobs and executes them.
type Scheduler struct {
	db       *sql.DB
	executor Executor
	mu       sync.Mutex
	stopCh   chan struct{}
	running  bool
}

// NewScheduler returns a ready scheduler.
func NewScheduler(db *sql.DB, executor Executor) *Scheduler {
	return &Scheduler{db: db, executor: executor, stopCh: make(chan struct{})}
}

// Start begins the background tick loop.
func (s *Scheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()
	go s.loop(ctx)
}

// Stop terminates the background loop.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return
	}
	s.running = false
	close(s.stopCh)
}

func (s *Scheduler) loop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Scheduler) tick(ctx context.Context) {
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, schedule, prompt, tool_policy, next_run
		 FROM cron_jobs WHERE enabled = 1 AND next_run <= ?`, now)
	if err != nil {
		return
	}
	defer rows.Close()
	var due []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.Name, &j.Schedule, &j.Prompt, &j.ToolPolicy, &j.NextRun); err != nil {
			continue
		}
		due = append(due, j)
	}
	rows.Close()
	for _, j := range due {
		go s.exec(j)
	}
}

func (s *Scheduler) exec(job Job) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	result, err := s.executor(ctx, job.Prompt, job.ToolPolicy)
	now := time.Now().UTC()
	next := ComputeNextRun(job.Schedule, now)

	status := ""
	if err != nil {
		status = fmt.Sprintf("ERROR: %v", err)
	} else {
		status = truncate(result, 1000)
	}

	_, _ = s.db.ExecContext(ctx,
		`UPDATE cron_jobs SET last_run=?, next_run=?, last_result=? WHERE id=?`,
		now.Format(time.RFC3339), next.Format(time.RFC3339), status, job.ID)
}

// ---------- CRUD ----------

// AddJob registers a new cron job.
func (s *Scheduler) AddJob(ctx context.Context, id, name, schedule, prompt, toolPolicy string) error {
	next := ComputeNextRun(schedule, time.Now().UTC())
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO cron_jobs (id,name,schedule,prompt,tool_policy,enabled,next_run,created_at)
		 VALUES (?,?,?,?,?,1,?,?)`,
		id, name, schedule, prompt, toolPolicy, next.Format(time.RFC3339), time.Now().UTC())
	return err
}

// RemoveJob deletes a job.
func (s *Scheduler) RemoveJob(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM cron_jobs WHERE id=?`, id)
	return err
}

// EnableJob toggles a job's enabled flag.
func (s *Scheduler) EnableJob(ctx context.Context, id string, enabled bool) error {
	v := 0
	if enabled {
		v = 1
	}
	_, err := s.db.ExecContext(ctx, `UPDATE cron_jobs SET enabled=? WHERE id=?`, v, id)
	return err
}

// ListJobs returns all jobs.
func (s *Scheduler) ListJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id,name,schedule,prompt,tool_policy,enabled,next_run,last_run,last_result,created_at
		 FROM cron_jobs ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		var j Job
		var enabled int
		var lastRun, lastResult sql.NullString
		if err := rows.Scan(&j.ID, &j.Name, &j.Schedule, &j.Prompt, &j.ToolPolicy,
			&enabled, &j.NextRun, &lastRun, &lastResult, &j.CreatedAt); err != nil {
			return nil, err
		}
		j.Enabled = enabled == 1
		if lastRun.Valid {
			t, _ := time.Parse(time.RFC3339, lastRun.String)
			j.LastRun = &t
		}
		if lastResult.Valid {
			j.LastResult = lastResult.String
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ---------- Cron expression ----------

// ComputeNextRun walks forward minute-by-minute to find the next time
// the schedule matches `from`.
func ComputeNextRun(schedule string, from time.Time) time.Time {
	fields := strings.Fields(schedule)
	if len(fields) != 5 {
		return from.Add(time.Minute)
	}
	next := from.Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < 366*24*60; i++ {
		if matchAll(fields, next) {
			return next
		}
		next = next.Add(time.Minute)
	}
	return from.Add(time.Hour)
}

func matchAll(fields []string, t time.Time) bool {
	return matchField(fields[0], t.Minute(), 0, 59) &&
		matchField(fields[1], t.Hour(), 0, 23) &&
		matchField(fields[2], t.Day(), 1, 31) &&
		matchField(fields[3], int(t.Month()), 1, 12) &&
		matchField(fields[4], int(t.Weekday()), 0, 6)
}

func matchField(field string, cur, lo, hi int) bool {
	if field == "*" {
		return true
	}
	if strings.HasPrefix(field, "*/") {
		step, err := strconv.Atoi(field[2:])
		return err == nil && step > 0 && cur%step == 0
	}
	if strings.Contains(field, "-") {
		p := strings.SplitN(field, "-", 2)
		a, _ := strconv.Atoi(p[0])
		b, _ := strconv.Atoi(p[1])
		return cur >= a && cur <= b
	}
	if strings.Contains(field, ",") {
		for _, p := range strings.Split(field, ",") {
			v, err := strconv.Atoi(strings.TrimSpace(p))
			if err == nil && v == cur {
				return true
			}
		}
		return false
	}
	v, err := strconv.Atoi(field)
	return err == nil && v == cur
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
