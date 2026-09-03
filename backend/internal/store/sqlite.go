package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
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

	CREATE TABLE IF NOT EXISTS provider_configs (
		name TEXT PRIMARY KEY,
		base_url TEXT NOT NULL DEFAULT '',
		api_key TEXT NOT NULL DEFAULT '',
		model TEXT NOT NULL DEFAULT '',
		headers_json TEXT NOT NULL DEFAULT '',
		updated_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS commit_decisions (
		source_key TEXT NOT NULL,
		commit_hash TEXT NOT NULL,
		decisions_json TEXT NOT NULL,
		updated_at DATETIME NOT NULL,
		PRIMARY KEY (source_key, commit_hash)
	);

	CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
	CREATE INDEX IF NOT EXISTS idx_jobs_created_at ON jobs(created_at DESC);
	`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}

	// Колонки usage добавляются к уже существующим таблицам (миграция без даунтайма).
	for _, stmt := range []string{
		`ALTER TABLE jobs ADD COLUMN prompt_tokens INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE jobs ADD COLUMN completion_tokens INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE jobs ADD COLUMN total_tokens INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := s.db.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
			return err
		}
	}

	return nil
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
		`SELECT id, status, created_at, started_at, finished_at, request_json, report_id, error, progress_json, prompt_tokens, completion_tokens, total_tokens FROM jobs WHERE id = ?`, id,
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
		`SELECT id, status, created_at, started_at, finished_at, request_json, report_id, error, progress_json, prompt_tokens, completion_tokens, total_tokens FROM jobs ORDER BY created_at DESC LIMIT ?`, limit,
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
		id               string
		status           string
		createdAt        time.Time
		startedAt        sql.NullTime
		finishedAt       sql.NullTime
		requestJSON      string
		reportID         sql.NullString
		errorMsg         sql.NullString
		progressJSON     sql.NullString
		promptTokens     int64
		completionTokens int64
		totalTokens      int64
	)

	err := row.Scan(&id, &status, &createdAt, &startedAt, &finishedAt,
		&requestJSON, &reportID, &errorMsg, &progressJSON,
		&promptTokens, &completionTokens, &totalTokens)
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
		ID:               id,
		Status:           Status(status),
		CreatedAt:        createdAt,
		Request:          req,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
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
		id               string
		status           string
		createdAt        time.Time
		startedAt        sql.NullTime
		finishedAt       sql.NullTime
		requestJSON      string
		reportID         sql.NullString
		errorMsg         sql.NullString
		progressJSON     sql.NullString
		promptTokens     int64
		completionTokens int64
		totalTokens      int64
	)

	err := rows.Scan(&id, &status, &createdAt, &startedAt, &finishedAt,
		&requestJSON, &reportID, &errorMsg, &progressJSON,
		&promptTokens, &completionTokens, &totalTokens)
	if err != nil {
		return nil, err
	}

	var req Request
	if err := json.Unmarshal([]byte(requestJSON), &req); err != nil {
		return nil, nil
	}

	j := &Job{
		ID:               id,
		Status:           Status(status),
		CreatedAt:        createdAt,
		Request:          req,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
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

func (s *SQLiteStore) ListProviderConfigs() ([]ProviderConfig, error) {
	rows, err := s.db.Query(
		`SELECT name, base_url, api_key, model, headers_json, updated_at FROM provider_configs ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ProviderConfig
	for rows.Next() {
		pc, err := scanProviderConfig(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, pc)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) GetProviderConfig(name string) (*ProviderConfig, bool) {
	var (
		baseURL     string
		apiKey      string
		model       string
		headersJSON sql.NullString
		updatedAt   time.Time
	)

	err := s.db.QueryRow(
		`SELECT base_url, api_key, model, headers_json, updated_at FROM provider_configs WHERE name = ?`,
		strings.TrimSpace(name),
	).Scan(&baseURL, &apiKey, &model, &headersJSON, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, false
		}
		return nil, false
	}

	pc := ProviderConfig{
		Name:      strings.TrimSpace(name),
		BaseURL:   baseURL,
		APIKey:    apiKey,
		Model:     model,
		UpdatedAt: updatedAt,
	}
	if headersJSON.Valid && headersJSON.String != "" {
		_ = json.Unmarshal([]byte(headersJSON.String), &pc.Headers)
	}
	return &pc, true
}

func (s *SQLiteStore) SaveProviderConfig(pc ProviderConfig) error {
	name := strings.TrimSpace(pc.Name)
	if name == "" {
		return fmt.Errorf("provider name is required")
	}

	headersJSON := ""
	if len(pc.Headers) > 0 {
		b, err := json.Marshal(pc.Headers)
		if err != nil {
			return fmt.Errorf("marshal headers: %w", err)
		}
		headersJSON = string(b)
	}

	_, err := s.db.Exec(
		`INSERT INTO provider_configs (name, base_url, api_key, model, headers_json, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET
			base_url = excluded.base_url,
			api_key = excluded.api_key,
			model = excluded.model,
			headers_json = excluded.headers_json,
			updated_at = excluded.updated_at`,
		name, strings.TrimSpace(pc.BaseURL), pc.APIKey, strings.TrimSpace(pc.Model), headersJSON, time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("upsert provider config: %w", err)
	}
	return nil
}

func (s *SQLiteStore) DeleteProviderConfig(name string) error {
	_, err := s.db.Exec(`DELETE FROM provider_configs WHERE name = ?`, strings.TrimSpace(name))
	return err
}

func (s *SQLiteStore) UpdateUsage(id string, prompt, completion, total int64) bool {
	res, err := s.db.Exec(
		`UPDATE jobs SET prompt_tokens = ?, completion_tokens = ?, total_tokens = ? WHERE id = ?`,
		prompt, completion, total, id,
	)
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

func (s *SQLiteStore) SaveCommitDecisions(sourceKey string, hashes []string, decisionsJSON []byte) error {
	if strings.TrimSpace(sourceKey) == "" || len(hashes) == 0 || len(decisionsJSON) == 0 {
		return nil
	}

	now := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(
		`INSERT INTO commit_decisions (source_key, commit_hash, decisions_json, updated_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT(source_key, commit_hash) DO UPDATE SET
			decisions_json = excluded.decisions_json,
			updated_at = excluded.updated_at`,
	)
	if err != nil {
		return fmt.Errorf("prepare stmt: %w", err)
	}
	defer stmt.Close()

	for _, h := range hashes {
		if h == "" {
			continue
		}
		if _, err := stmt.Exec(sourceKey, h, string(decisionsJSON), now); err != nil {
			return fmt.Errorf("upsert commit decisions: %w", err)
		}
	}

	return tx.Commit()
}

func (s *SQLiteStore) LoadCommitDecisions(sourceKey string, hashes []string) (map[string][]byte, error) {
	out := make(map[string][]byte, len(hashes))
	if strings.TrimSpace(sourceKey) == "" || len(hashes) == 0 {
		return out, nil
	}

	const chunkSize = 400
	for start := 0; start < len(hashes); start += chunkSize {
		end := start + chunkSize
		if end > len(hashes) {
			end = len(hashes)
		}
		chunk := hashes[start:end]

		placeholders := strings.TrimRight(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, 0, len(chunk)+1)
		args = append(args, sourceKey)
		for _, h := range chunk {
			args = append(args, h)
		}

		rows, err := s.db.Query(
			`SELECT commit_hash, decisions_json FROM commit_decisions WHERE source_key = ? AND commit_hash IN (`+placeholders+`)`,
			args...,
		)
		if err != nil {
			return nil, err
		}

		for rows.Next() {
			var hash, data string
			if err := rows.Scan(&hash, &data); err != nil {
				rows.Close()
				return nil, err
			}
			out[hash] = []byte(data)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	return out, nil
}

func scanProviderConfig(scan func(dest ...any) error) (ProviderConfig, error) {
	var (
		pc          ProviderConfig
		headersJSON sql.NullString
		updatedAt   time.Time
	)

	err := scan(&pc.Name, &pc.BaseURL, &pc.APIKey, &pc.Model, &headersJSON, &updatedAt)
	if err != nil {
		return pc, err
	}
	pc.UpdatedAt = updatedAt
	if headersJSON.Valid && headersJSON.String != "" {
		_ = json.Unmarshal([]byte(headersJSON.String), &pc.Headers)
	}
	return pc, nil
}
