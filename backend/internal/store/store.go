package store

import (
	"time"
)

type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

type Progress struct {
	Stage          string `json:"stage"`
	Message        string `json:"message"`
	Details        string `json:"details,omitempty"`
	TotalCommits   int    `json:"total_commits,omitempty"`
	ProcessedItems int    `json:"processed_items,omitempty"`
	TotalBatches   int    `json:"total_batches,omitempty"`
	DoneBatches    int    `json:"done_batches,omitempty"`
	TotalReduce    int    `json:"total_reduce,omitempty"`
	DoneReduce     int    `json:"done_reduce,omitempty"`
}

type Request struct {
	SourceType string `json:"source_type"`
	Source     string `json:"source"`
	Provider   string `json:"provider"`
	Model      string `json:"model,omitempty"`
	Limit      int    `json:"limit"`
	Language   string `json:"language"`
	Cascade    bool   `json:"cascade"`
	Diff       bool   `json:"diff"`
	ReportType string `json:"report_type,omitempty"`

	Since      string `json:"since,omitempty"`
	Until      string `json:"until,omitempty"`
	FromCommit string `json:"from_commit,omitempty"`
	ToCommit   string `json:"to_commit,omitempty"`
}

type Job struct {
	ID         string    `json:"id"`
	Status     Status    `json:"status"`
	CreatedAt  time.Time `json:"created_at"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at"`
	Request    Request   `json:"request"`
	ReportID   string    `json:"report_id,omitempty"`
	Error      string    `json:"error,omitempty"`
	Progress   *Progress `json:"progress,omitempty"`
}

type Report struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Markdown  string    `json:"markdown"`
}

// ProviderConfig — снимок настроек провайдера, сохранённый пользователем.
// Пустые значения означают «не задано».
type ProviderConfig struct {
	Name      string            `json:"name"`
	BaseURL   string            `json:"base_url,omitempty"`
	APIKey    string            `json:"api_key,omitempty"`
	Model     string            `json:"model,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	UpdatedAt time.Time         `json:"updated_at"`
}

type Store interface {
	// Jobs
	CreateJob(req Request) (*Job, error)
	GetJob(id string) (*Job, bool)
	ListJobs(limit int) ([]*Job, error)
	MarkRunning(id string) bool
	MarkFailed(id, publicErr string) bool
	MarkCompleted(id, reportID string) bool
	UpdateProgress(id string, progress *Progress) bool
	DeleteJob(id string) bool

	// Reports
	SaveReport(markdown string) (*Report, error)
	GetReport(id string) (*Report, bool)

	// Provider settings
	ListProviderConfigs() ([]ProviderConfig, error)
	GetProviderConfig(name string) (*ProviderConfig, bool)
	SaveProviderConfig(pc ProviderConfig) error
	DeleteProviderConfig(name string) error

	// Lifecycle
	Close() error
}
