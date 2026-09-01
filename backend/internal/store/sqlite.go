package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	if path == "" {
		path = "data.db"
	}

	dsn := fmt.Sprintf("file:%s?_journal=WAL&_timeout=5000&_fk=true", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open sqlite: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(2)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping sqlite: %w", err)
	}

	s := &SQLiteStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	return s, nil
}

func (s *SQLiteStore) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *SQLiteStore) migrate() error {
	schema := `
	CREATE TABLE IF NOT EXISTS jobs (
		id TEXT PRIMARY KEY,
		status TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		started_at DATETIME,
		finished_at DATETIME,
		request_json TEXT NOT NULL,
		report_id TEXT,
		error TEXT,
		progress_json TEXT
	);

	CREATE TABLE IF NOT EXISTS reports (
		id TEXT PRIMARY KEY,
		created_at DATETIME NOT NULL,
		markdown TEXT NOT NULL
	);

	CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
	CREATE INDEX IF NOT EXISTS idx_jobs_created_at ON jobs(created_at DESC);
	`
	_, err := s.db.Exec(schema)
	return err
}

func newID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *SQLiteStore) CreateJob(req Request) (*Job, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	reqJSON, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	_, err = s.db.Exec(
		`INSERT INTO jobs (id, status, created_at, request_json) VALUES (?, ?, ?, ?)`,
		id, string(StatusQueued), now, string(reqJSON),
	)
	if err != nil {
		return nil, fmt.Errorf("insert job: %w", err)
	}

	return &Job{
		ID:        id,
		Status:    StatusQueued,
		CreatedAt: now,
		Request:   req,
	}, nil
}

func (s *SQLiteStore) GetJob(id string) (*Job, bool) {
	row := s.db.QueryRow(
		`SELECT id, status, created_at, started_at, finished_at, request_json, report_id, error, progress_json 
		 FROM jobs WHERE id = ?`, id,
	)
	return s.scanJob(row)
}

func (s *SQLiteStore) ListJobs(limit int) ([]*Job, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}

	rows, err := s.db.Query(
		`SELECT id, status, created_at, started_at, finished_at, request_json, report_id, error, progress_json 
		 FROM jobs ORDER BY created_at DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*Job
	for rows.Next() {
		j, err := s.scanJobFromRows(rows)
		if err != nil {
			return nil, err
		}
		if j != nil {
			jobs = append(jobs, j)
		}
	}
	return jobs, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func (s *SQLiteStore) scanJob(row rowScanner) (*Job, bool) {
	var (
		id           string
		status       string
		createdAt    time.Time
		startedAt    sql.NullTime
		finishedAt   sql.NullTime
		requestJSON  string
		reportID     sql.NullString
		errorMsg     sql.NullString
		progressJSON sql.NullString
	)

	err := row.Scan(&id, &status, &createdAt, &startedAt, &finishedAt,
		&requestJSON, &reportID, &errorMsg, &progressJSON)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, false
		}
		return nil, false
	}

	var req Request
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return nil, false
	}

	j := &Job{
		ID:        id,
		Status:    Status(status),
		CreatedAt: createdAt,
		Request:   req,
	}

	if startedAt.Valid {
		j.StartedAt = startedAt.Time
	}
	if finishedAt.Valid {
		j.FinishedAt = finishedAt.Time
	}
	if reportID.Valid {
		j.ReportID = reportID.String
	}
	if errorMsg.Valid {
		j.Error = errorMsg.String
	}
	if progressJSON.Valid && progressJSON.String != "" {
		var p Progress
		if err := json.Unmarshal([]byte(progressJSON.String), &p); err == nil {
			j.Progress = &p
		}
	}

	return j, true
}

func (s *SQLiteStore) scanJobFromRows(rows *sql.Rows) (*Job, error) {
	var (
		id           string
		status       string
		createdAt    time.Time
		startedAt    sql.NullTime
		finishedAt   sql.NullTime
		requestJSON  string
		reportID     sql.NullString
		errorMsg     sql.NullString
		progressJSON sql.NullString
	)

	err := rows.Scan(&id, &status, &createdAt, &startedAt, &finishedAt,
		&requestJSON, &reportID, &errorMsg, &progressJSON)
	if err != nil {
		return nil, err
	}

	var req Request
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return nil, nil
	}

	j := &Job{
		ID:        id,
		Status:    Status(status),
		CreatedAt: createdAt,
		Request:   req,
	}
	if startedAt.Valid {
		j.StartedAt = startedAt.Time
	}
	if finishedAt.Valid {
		j.FinishedAt = finishedAt.Time
	}
	if reportID.Valid {
		j.ReportID = reportID.String
	}
	if errorMsg.Valid {
		j.Error = errorMsg.String
	}
	if progressJSON.Valid && progressJSON.String != "" {
		var p Progress
		if err := json.Unmarshal([]byte(progressJSON.String), &p); err == nil {
			j.Progress = &p
		}
	}
	return j, nil
}

func (s *SQLiteStore) MarkRunning(id string) bool {
	res, err := s.db.Exec(
		`UPDATE jobs SET status = ?, started_at = ? WHERE id = ?`,
		string(StatusRunning), time.Now().UTC(), id,
	)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func (s *SQLiteStore) MarkFailed(id, publicErr string) bool {
	res, err := s.db.Exec(
		`UPDATE jobs SET status = ?, error = ?, finished_at = ? WHERE id = ?`,
		string(StatusFailed), publicErr, time.Now().UTC(), id,
	)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func (s *SQLiteStore) MarkCompleted(id, reportID string) bool {
	res, err := s.db.Exec(
		`UPDATE jobs SET status = ?, report_id = ?, finished_at = ? WHERE id = ?`,
		string(StatusCompleted), reportID, time.Now().UTC(), id,
	)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func (s *SQLiteStore) UpdateProgress(id string, progress *Progress) bool {
	var progressJSON string
	if progress != nil {
		if b, err := json.Marshal(progress); err == nil {
			progressJSON = string(b)
		}
	}
	res, err := s.db.Exec(`UPDATE jobs SET progress_json = ? WHERE id = ?`, progressJSON, id)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func (s *SQLiteStore) SaveReport(markdown string) (*Report, error) {
	id, err := newID()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	_, err = s.db.Exec(
		`INSERT INTO reports (id, created_at, markdown) VALUES (?, ?, ?)`,
		id, now, markdown,
	)
	if err != nil {
		return nil, fmt.Errorf("insert report: %w", err)
	}

	return &Report{
		ID:        id,
		CreatedAt: now,
		Markdown:  markdown,
	}, nil
}

func (s *SQLiteStore) GetReport(id string) (*Report, bool) {
	var createdAt time.Time
	var markdown string

	err := s.db.QueryRow(
		`SELECT created_at, markdown FROM reports WHERE id = ?`, id,
	).Scan(&createdAt, &markdown)
	if err != nil {
		return nil, false
	}

	return &Report{
		ID:        id,
		CreatedAt: createdAt,
		Markdown:  markdown,
	}, true
}

func (s *SQLiteStore) DeleteJob(id string) bool {
	res, err := s.db.Exec(`DELETE FROM jobs WHERE id = ?`, id)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}
