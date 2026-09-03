package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"

	"code-archaeologist/backend/internal/analyzer"
	"code-archaeologist/backend/internal/config"
	"code-archaeologist/backend/internal/llm"
	"code-archaeologist/backend/internal/scanner"
	"code-archaeologist/backend/internal/store"
)

var urlCredsRe = regexp.MustCompile(`(https?://)[^@/]+@`)

type API struct {
	cfg     *config.Config
	store   store.Store
	scanner *scanner.Service
	jobSem  chan struct{}
}

func New(cfg *config.Config, s store.Store) *API {
	return &API{
		cfg:     cfg,
		store:   s,
		scanner: scanner.NewService(cfg.Git),
		jobSem:  make(chan struct{}, cfg.Analysis.MaxConcurrentJobs),
	}
}

func (a *API) Analyze(c *gin.Context) {
	var req struct {
		SourceType string `json:"source_type" binding:"required"`
		Source     string `json:"source" binding:"required"`
		Provider   string `json:"provider"`
		Model      string `json:"model"`
		Limit      int    `json:"limit"`
		Language   string `json:"language"`
		Cascade    *bool  `json:"cascade"`
		Diff       *bool  `json:"diff"`
		ReportType string `json:"report_type"`

		Since      string `json:"since"`
		Until      string `json:"until"`
		FromCommit string `json:"from_commit"`
		ToCommit   string `json:"to_commit"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	sourceType := strings.ToLower(strings.TrimSpace(req.SourceType))
	switch sourceType {
	case "local", "github", "gitlab":
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "source_type must be local, github or gitlab"})
		return
	}

	source := strings.TrimSpace(req.Source)
	if source == "" || len(source) > 2048 || strings.HasPrefix(source, "-") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid source"})
		return
	}

	provider := strings.ToLower(strings.TrimSpace(req.Provider))
	if provider == "" {
		provider = a.cfg.DefaultProvider
	}
	if !llm.IsSupported(provider) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported provider"})
		return
	}

	model := strings.TrimSpace(req.Model)
	if len(model) > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "model is too long"})
		return
	}

	language := strings.TrimSpace(req.Language)
	if language == "" {
		language = "ru"
	}

	reportType := analyzer.NormalizeReportType(req.ReportType)
	if !analyzer.IsSupportedReportType(reportType) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported report type"})
		return
	}

	filter := scanner.CommitFilter{
		Since:      strings.TrimSpace(req.Since),
		Until:      strings.TrimSpace(req.Until),
		FromCommit: strings.TrimSpace(req.FromCommit),
		ToCommit:   strings.TrimSpace(req.ToCommit),
	}
	if err := scanner.ValidateCommitFilter(filter); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 0
	}
	if limit > a.cfg.Git.MaxCommits {
		limit = a.cfg.Git.MaxCommits
	}

	cascade := a.cfg.Cascade.Enabled
	if req.Cascade != nil {
		cascade = *req.Cascade
	}

	diff := a.cfg.Diff.Enabled
	if req.Diff != nil {
		diff = *req.Diff
	}

	jobReq := store.Request{
		SourceType: sourceType,
		Source:     redactSource(sourceType, source),
		Provider:   llm.NormalizeProvider(provider),
		Model:      model,
		Limit:      limit,
		Language:   language,
		Cascade:    cascade,
		Diff:       diff,
		ReportType: reportType,
		Since:      filter.Since,
		Until:      filter.Until,
		FromCommit: filter.FromCommit,
		ToCommit:   filter.ToCommit,
	}

	job, err := a.store.CreateJob(jobReq)
	if err != nil {
		log.Printf("[Handlers] failed to create job: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create job"})
		return
	}

	go a.runJob(job.ID, jobReq, source)

	c.JSON(http.StatusAccepted, gin.H{
		"job_id": job.ID,
		"status": job.Status,
	})
}

func (a *API) Job(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "job id is required"})
		return
	}

	job, ok := a.store.GetJob(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}

	c.JSON(http.StatusOK, job)
}

func (a *API) Report(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "report id is required"})
		return
	}

	report, ok := a.store.GetReport(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "report not found"})
		return
	}

	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/markdown; charset=utf-8", []byte(report.Markdown))
}

func (a *API) Providers(c *gin.Context) {
	type providerStatus struct {
		Name       string `json:"name"`
		Configured bool   `json:"configured"`
	}

	list := []providerStatus{
		{Name: "ollama", Configured: a.cfg.Ollama.BaseURL != ""},
		{Name: "llamacpp", Configured: a.cfg.LlamaCpp.BaseURL != ""},
		{Name: "openai", Configured: a.cfg.OpenAI.BaseURL != "" && a.cfg.OpenAI.APIKey != ""},
		{Name: "deepseek", Configured: a.cfg.DeepSeek.BaseURL != "" && a.cfg.DeepSeek.APIKey != ""},
		{Name: "qwen", Configured: a.cfg.Qwen.BaseURL != "" && a.cfg.Qwen.APIKey != ""},
		{Name: "custom", Configured: a.cfg.Custom.BaseURL != ""},
	}

	c.JSON(http.StatusOK, gin.H{
		"default":   a.cfg.DefaultProvider,
		"providers": list,
	})
}

func (a *API) runJob(id string, req store.Request, rawSource string) {
	a.jobSem <- struct{}{}
	defer func() { <-a.jobSem }()

	if !a.store.MarkRunning(id) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Analysis.JobTimeout)
	defer cancel()

	provider, err := llm.NewProvider(req.Provider, a.cfg)
	if err != nil {
		a.failJob(id, "provider configuration failed", err)
		return
	}

	repo, err := a.scanner.Prepare(ctx, req.SourceType, rawSource)
	if err != nil {
		a.failJob(id, "repository preparation failed", err)
		return
	}
	defer repo.Cleanup()

	limit := req.Limit
	if limit <= 0 {
		limit = a.cfg.Git.MaxCommits
	}

	commits, err := a.scanner.Commits(ctx, repo.Path, limit, scanner.CommitFilter{
		Since:      req.Since,
		Until:      req.Until,
		FromCommit: req.FromCommit,
		ToCommit:   req.ToCommit,
	})
	if err != nil {
		a.failJob(id, "git history extraction failed", err)
		return
	}

	a.store.UpdateProgress(id, &store.Progress{
		Stage:   "preparing",
		Message: "Извлечение diff коммитов",
	})

	a.store.UpdateProgress(id, &store.Progress{
		Stage:   "extracting_commits",
		Message: "Анализ истории Git",
		Details: fmt.Sprintf("Найдено коммитов: %d", len(commits)),
	})

	var commitsWithDiff []scanner.CommitWithDiff
	if req.Diff {
		a.store.UpdateProgress(id, &store.Progress{
			Stage:        "extracting_diffs",
			Message:      "Извлечение изменений кода (diff)",
			TotalCommits: len(commits),
		})
		commitsWithDiff, err = a.scanner.CommitsWithDiff(ctx, repo.Path, commits, a.cfg.Diff.MaxSizePerCommit, func(current, total int, hash string) {
			shortHash := hash
			if len(shortHash) > 8 {
				shortHash = shortHash[:8]
			}
			a.store.UpdateProgress(id, &store.Progress{
				Stage:          "extracting_diffs",
				Message:        "Извлечение изменений кода (diff)",
				Details:        fmt.Sprintf("Обработка коммита %s", shortHash),
				TotalCommits:   total,
				ProcessedItems: current,
			})
		})
	} else {
		commitsWithDiff = make([]scanner.CommitWithDiff, len(commits))
		for i, c := range commits {
			commitsWithDiff[i] = scanner.CommitWithDiff{Commit: c}
		}
	}

	modelName := req.Model
	if modelName == "" {
		modelName = provider.DefaultModel()
	}

	params := analyzer.Params{
		SourceType:   req.SourceType,
		Source:       req.Source,
		ProviderName: provider.Name(),
		Model:        modelName,
		Language:     req.Language,
		ReportType:   req.ReportType,
		Since:        req.Since,
		Until:        req.Until,
		FromCommit:   req.FromCommit,
		ToCommit:     req.ToCommit,
	}

	var markdown string

	if req.Cascade {
		cascadeCfg := analyzer.CascadeConfig{
			Enabled:     true,
			MaxParallel: a.cfg.Cascade.MaxParallel,
			ReduceSize:  a.cfg.Cascade.ReduceSize,
			BatchSize:   a.cfg.Analysis.BatchSize,
		}

		markdown, err = analyzer.RunCascade(ctx, provider, params, commitsWithDiff, cascadeCfg, func(stage, message, details string, totalBatches, doneBatches, totalReduce, doneReduce int) {
			a.store.UpdateProgress(id, &store.Progress{
				Stage:        stage,
				TotalBatches: totalBatches,
				DoneBatches:  doneBatches,
				TotalReduce:  totalReduce,
				DoneReduce:   doneReduce,
				Message:      fmt.Sprintf("%s: %s", message, details),
			})
		})
	} else {
		markdown, err = analyzer.Run(ctx, provider, params, commitsWithDiff, a.cfg.Analysis.BatchSize)
	}

	if err != nil {
		a.failJob(id, "analysis failed", err)
		return
	}

	report, err := a.store.SaveReport(markdown)
	if err != nil {
		a.failJob(id, "report save failed", err)
		return
	}

	a.store.MarkCompleted(id, report.ID)
	log.Printf("[Job:%s] completed report_id=%s", id, report.ID)
}

func (a *API) Jobs(c *gin.Context) {
	jobs, err := a.store.ListJobs(100)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list jobs"})
		return
	}
	if jobs == nil {
		jobs = []*store.Job{}
	}
	c.JSON(http.StatusOK, jobs)
}

func (a *API) failJob(id, publicStage string, err error) {
	log.Printf("[Job:%s] %s: %s", id, publicStage, sanitizeError(err))
	a.store.MarkFailed(id, publicStage)
}

func redactSource(sourceType, source string) string {
	if sourceType == "local" {
		return source
	}

	u, err := url.Parse(source)
	if err == nil && u.User != nil {
		u.User = url.User("redacted")
		return u.String()
	}

	return source
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	return urlCredsRe.ReplaceAllString(err.Error(), "${1}redacted@")
}

func (a *API) DeleteJob(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "job id is required"})
		return
	}

	job, ok := a.store.GetJob(id)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}

	if job.Status == store.StatusRunning {
		c.JSON(http.StatusConflict, gin.H{"error": "cannot delete a running job, wait for it to finish or fail"})
		return
	}

	if !a.store.DeleteJob(id) {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete job"})
		return
	}

	c.Status(http.StatusNoContent)
}
