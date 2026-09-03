package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"code-archaeologist/backend/internal/analyzer"
	"code-archaeologist/backend/internal/config"
	"code-archaeologist/backend/internal/llm"
	"code-archaeologist/backend/internal/providers"
	"code-archaeologist/backend/internal/scanner"
	"code-archaeologist/backend/internal/store"
)

var urlCredsRe = regexp.MustCompile(`(https?://)[^@/]+@`)

type API struct {
	cfg       *config.Config
	store     store.Store
	scanner   *scanner.Service
	providers *providers.Manager
	jobSem    chan struct{}
	sseMu     sync.Mutex
	sse       map[string]map[chan []byte]struct{}
}

func New(cfg *config.Config, s store.Store) *API {
	return &API{
		cfg:       cfg,
		store:     s,
		scanner:   scanner.NewService(cfg.Git),
		providers: providers.NewManager(cfg, s),
		jobSem:    make(chan struct{}, cfg.Analysis.MaxConcurrentJobs),
		sse:       make(map[string]map[chan []byte]struct{}),
	}
}

// publishJobEvent отправляет событие всем SSE-подписчикам задачи (неблокирующе).
func (a *API) publishJobEvent(jobID string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	a.sseMu.Lock()
	defer a.sseMu.Unlock()
	for ch := range a.sse[jobID] {
		select {
		case ch <- data:
		default:
		}
	}
}

// jobEventHandler оборачивает store-вызовы прогресса публикацией SSE-событий.
func (a *API) updateProgress(id string, p *store.Progress) {
	a.store.UpdateProgress(id, p)
	a.publishJobEvent(id, gin.H{
		"type":     "progress",
		"job_id":   id,
		"status":   string(store.StatusRunning),
		"progress": p,
	})
}

func (a *API) Analyze(c *gin.Context) {
	var req struct {
		SourceType  string `json:"source_type" binding:"required"`
		Source      string `json:"source" binding:"required"`
		Provider    string `json:"provider"`
		Model       string `json:"model"`
		Limit       int    `json:"limit"`
		Language    string `json:"language"`
		Cascade     *bool  `json:"cascade"`
		Diff        *bool  `json:"diff"`
		ReportType  string `json:"report_type"`
		Incremental *bool  `json:"incremental"`

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
	if !a.providers.Knows(provider) {
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

	incremental := a.cfg.Analysis.IncrementalEnabled
	if req.Incremental != nil {
		incremental = *req.Incremental
	}

	jobReq := store.Request{
		SourceType:  sourceType,
		Source:      redactSource(sourceType, source),
		Provider:    llm.NormalizeProvider(provider),
		Model:       model,
		Limit:       limit,
		Language:    language,
		Cascade:     cascade,
		Diff:        diff,
		ReportType:  reportType,
		Incremental: incremental,
		Since:       filter.Since,
		Until:       filter.Until,
		FromCommit:  filter.FromCommit,
		ToCommit:    filter.ToCommit,
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

// JobEvents — SSE-поток прогресса задачи.
func (a *API) JobEvents(c *gin.Context) {
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

	ch := make(chan []byte, 32)
	a.sseMu.Lock()
	if a.sse[id] == nil {
		a.sse[id] = make(map[chan []byte]struct{})
	}
	a.sse[id][ch] = struct{}{}
	a.sseMu.Unlock()

	defer func() {
		a.sseMu.Lock()
		delete(a.sse[id], ch)
		if len(a.sse[id]) == 0 {
			delete(a.sse, id)
		}
		a.sseMu.Unlock()
		close(ch)
	}()

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	// Текущее состояние — первым событием, чтобы клиент сразу отрисовал прогресс.
	if snapshot, err := json.Marshal(gin.H{
		"type":     "snapshot",
		"job_id":   id,
		"status":   string(job.Status),
		"progress": job.Progress,
	}); err == nil {
		c.Writer.WriteString("data: " + string(snapshot) + "\n\n")
		flusher.Flush()
	}

	if job.Status != store.StatusRunning && job.Status != store.StatusQueued {
		return
	}

	clientGone := c.Request.Context().Done()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case data, ok := <-ch:
			if !ok {
				return
			}
			if _, err := c.Writer.WriteString("data: " + string(data) + "\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := c.Writer.WriteString(": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-clientGone:
			return
		}
	}
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
	list, err := a.providers.List()
	if err != nil {
		log.Printf("[Handlers] failed to list providers: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list providers"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"default":   a.cfg.DefaultProvider,
		"providers": list,
	})
}

func (a *API) CreateProvider(c *gin.Context) {
	var in providers.UpdateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	name := strings.TrimSpace(c.Param("name"))
	info, err := a.providers.Create(name, in)
	if err != nil {
		respondProviderError(c, err)
		return
	}
	c.JSON(http.StatusCreated, info)
}

func (a *API) UpdateProvider(c *gin.Context) {
	var in providers.UpdateInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	name := strings.TrimSpace(c.Param("name"))
	info, err := a.providers.Update(name, in)
	if err != nil {
		respondProviderError(c, err)
		return
	}
	c.JSON(http.StatusOK, info)
}

func (a *API) DeleteProvider(c *gin.Context) {
	name := strings.TrimSpace(c.Param("name"))
	if err := a.providers.Delete(name); err != nil {
		respondProviderError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (a *API) TestProvider(c *gin.Context) {
	name := strings.TrimSpace(c.Param("name"))

	reply, err := a.providers.Test(c.Request.Context(), name)
	if err != nil {
		msg := sanitizeError(err)
		if sc, rErr := a.providers.Resolve(name); rErr == nil && sc.APIKey != "" {
			msg = strings.ReplaceAll(msg, sc.APIKey, "***")
		}
		c.JSON(http.StatusOK, gin.H{"ok": false, "error": truncateForUI(msg, 400)})
		return
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "reply": truncateForUI(reply, 200)})
}

func truncateForUI(s string, limit int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= limit {
		return string(r)
	}
	return string(r[:limit]) + "…"
}

func respondProviderError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, providers.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
	case errors.Is(err, providers.ErrConflict):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	}
}

// buildChainable собирает цепочку: retry(primary) + fallback-провайдеры с retry.
func (a *API) buildChainable(primaryName string) (llm.Provider, error) {
	primary, err := a.providers.Build(primaryName)
	if err != nil {
		return nil, err
	}

	wrapped := llm.WithRetries(primary, a.cfg.Analysis.MaxRetries, 2*time.Second)

	var fallbacks []llm.Provider
	for _, name := range a.cfg.Analysis.FallbackProviders {
		name = llm.NormalizeProvider(name)
		if name == "" || name == llm.NormalizeProvider(primaryName) {
			continue
		}
		if !a.providers.Knows(name) {
			log.Printf("[Job] fallback provider %q is not configured, skipping", name)
			continue
		}
		fp, err := a.providers.Build(name)
		if err != nil {
			log.Printf("[Job] fallback provider %q failed to build, skipping: %v", name, err)
			continue
		}
		fallbacks = append(fallbacks, llm.WithRetries(fp, a.cfg.Analysis.MaxRetries, 2*time.Second))
	}

	return llm.WithFallback(wrapped, fallbacks...), nil
}

func (a *API) runJob(id string, req store.Request, rawSource string) {
	a.jobSem <- struct{}{}
	defer func() { <-a.jobSem }()

	// Паника в анализе не должна ронять процесс.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[Job:%s] panic recovered: %v", id, r)
			a.store.MarkFailed(id, "internal error: analysis panicked")
		}
	}()

	if !a.store.MarkRunning(id) {
		return
	}

	a.publishJobEvent(id, gin.H{"type": "status", "job_id": id, "status": string(store.StatusRunning)})

	ctx, cancel := context.WithTimeout(context.Background(), a.cfg.Analysis.JobTimeout)
	defer cancel()

	provider, err := a.buildChainable(req.Provider)
	if err != nil {
		a.failJob(id, "provider configuration failed", err)
		return
	}
	counter := llm.WithCounting(provider)

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

	a.updateProgress(id, &store.Progress{
		Stage:   "preparing",
		Message: "Извлечение diff коммитов",
	})

	a.updateProgress(id, &store.Progress{
		Stage:   "extracting_commits",
		Message: "Анализ истории Git",
		Details: fmt.Sprintf("Найдено коммитов: %d", len(commits)),
	})

	var commitsWithDiff []scanner.CommitWithDiff
	if req.Diff {
		a.updateProgress(id, &store.Progress{
			Stage:        "extracting_diffs",
			Message:      "Извлечение изменений кода (diff)",
			TotalCommits: len(commits),
		})
		commitsWithDiff, err = a.scanner.CommitsWithDiff(ctx, repo.Path, commits, a.cfg.Diff.MaxSizePerCommit, func(current, total int, hash string) {
			shortHash := hash
			if len(shortHash) > 8 {
				shortHash = shortHash[:8]
			}
			a.updateProgress(id, &store.Progress{
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
		SourceKey:    sourceKeyFor(req),
		Incremental:  req.Incremental,
		Cache:        &storeDecisionCache{st: a.store},
	}

	var markdown string

	if req.Cascade {
		cascadeCfg := analyzer.CascadeConfig{
			Enabled:     true,
			MaxParallel: a.cfg.Cascade.MaxParallel,
			ReduceSize:  a.cfg.Cascade.ReduceSize,
			BatchSize:   a.cfg.Analysis.BatchSize,
		}

		markdown, err = analyzer.RunCascade(ctx, counter, params, commitsWithDiff, cascadeCfg, func(stage, message, details string, totalBatches, doneBatches, totalReduce, doneReduce int) {
			a.updateProgress(id, &store.Progress{
				Stage:        stage,
				TotalBatches: totalBatches,
				DoneBatches:  doneBatches,
				TotalReduce:  totalReduce,
				DoneReduce:   doneReduce,
				Message:      fmt.Sprintf("%s: %s", message, details),
			})
		})
	} else {
		markdown, err = analyzer.Run(ctx, counter, params, commitsWithDiff, a.cfg.Analysis.BatchSize)
	}

	usage := counter.Snapshot()
	a.store.UpdateUsage(id, usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)

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
	a.publishJobEvent(id, gin.H{
		"type":      "status",
		"job_id":    id,
		"status":    string(store.StatusCompleted),
		"report_id": report.ID,
	})
	log.Printf("[Job:%s] completed report_id=%s tokens=%d requests=%d", id, report.ID, usage.TotalTokens, counter.Requests())
}

// sourceKeyFor — ключ инкрементального кэша: источник + тип отчёта + фильтры.
func sourceKeyFor(req store.Request) string {
	return strings.Join([]string{
		req.SourceType,
		req.Source,
		req.ReportType,
		req.Since,
		req.Until,
		req.FromCommit,
		req.ToCommit,
	}, "|")
}

// storeDecisionCache адаптирует store к analyzer.DecisionCache.
type storeDecisionCache struct {
	st store.Store
}

func (c *storeDecisionCache) Load(sourceKey string, hashes []string) (map[string][]byte, error) {
	return c.st.LoadCommitDecisions(sourceKey, hashes)
}

func (c *storeDecisionCache) Save(sourceKey string, hashes []string, decisionsJSON []byte) error {
	return c.st.SaveCommitDecisions(sourceKey, hashes, decisionsJSON)
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
	a.publishJobEvent(id, gin.H{"type": "status", "job_id": id, "status": string(store.StatusFailed), "error": publicStage})
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
