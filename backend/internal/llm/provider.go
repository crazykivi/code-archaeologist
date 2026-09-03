package llm

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"code-archaeologist/backend/internal/config"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatOptions struct {
	Model       string
	Temperature float64
	MaxTokens   int
}

// Response — результат одного вызова Chat.
type Response struct {
	Content string
	Usage   Usage
}

// Usage — потребление токенов за один запрос (нули, если провайдер их не отдаёт).
type Usage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type Provider interface {
	Name() string
	DefaultModel() string
	Chat(ctx context.Context, messages []Message, opts ChatOptions) (Response, error)
}

// HTTPError — ошибка HTTP-ответа провайдера; позволяет отличить 429/5xx от остальных.
type HTTPError struct {
	Provider string
	Status   int
	Body     string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s provider returned HTTP %d: %s", e.Provider, e.Status, truncateString(e.Body, 512))
}

// StatusCodeOf извлекает HTTP-статус из ошибки, если это ошибка провайдера.
func StatusCodeOf(err error) (int, bool) {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status, true
	}
	return 0, false
}

func NormalizeProvider(name string) string {
	p := strings.ToLower(strings.TrimSpace(name))
	switch p {
	case "gpt", "openai":
		return "openai"
	case "llama.cpp", "llama-cpp", "llamacpp":
		return "llamacpp"
	case "dashscope":
		return "qwen"
	default:
		return p
	}
}

// BuiltinProviders — встроенные имена провайдеров (настраиваются через env, переопределяются из БД).
func BuiltinProviders() []string {
	return []string{"ollama", "llamacpp", "openai", "deepseek", "qwen", "custom"}
}

func IsBuiltin(name string) bool {
	name = NormalizeProvider(name)
	for _, b := range BuiltinProviders() {
		if name == b {
			return true
		}
	}
	return false
}

// NewFromStatic собирает провайдера из уже разрешённой конфигурации.
// Все провайдеры, включая Ollama, используют OpenAI-совместимый chat completions API.
func NewFromStatic(cfg StaticConfig, timeout time.Duration) (Provider, error) {
	cfg.Name = NormalizeProvider(cfg.Name)
	return NewOpenAICompatible(cfg, timeout)
}

// NewProvider — сборка провайдера только из env-конфигурации (без настроек из БД).
func NewProvider(name string, cfg *config.Config) (Provider, error) {
	name = NormalizeProvider(name)
	timeout := cfg.Analysis.RequestTimeout

	static := StaticConfig{Name: name}
	switch name {
	case "ollama":
		static.BaseURL = cfg.Ollama.BaseURL
		static.Model = cfg.Ollama.Model
	case "llamacpp":
		static.BaseURL = cfg.LlamaCpp.BaseURL
		static.APIKey = cfg.LlamaCpp.APIKey
		static.Model = cfg.LlamaCpp.Model
	case "openai":
		if cfg.OpenAI.APIKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY is required")
		}
		static.BaseURL = cfg.OpenAI.BaseURL
		static.APIKey = cfg.OpenAI.APIKey
		static.Model = cfg.OpenAI.Model
	case "deepseek":
		if cfg.DeepSeek.APIKey == "" {
			return nil, fmt.Errorf("DEEPSEEK_API_KEY is required")
		}
		static.BaseURL = cfg.DeepSeek.BaseURL
		static.APIKey = cfg.DeepSeek.APIKey
		static.Model = cfg.DeepSeek.Model
	case "qwen":
		if cfg.Qwen.APIKey == "" {
			return nil, fmt.Errorf("DASHSCOPE_API_KEY is required")
		}
		static.BaseURL = cfg.Qwen.BaseURL
		static.APIKey = cfg.Qwen.APIKey
		static.Model = cfg.Qwen.Model
	case "custom":
		if cfg.Custom.BaseURL == "" {
			return nil, fmt.Errorf("CUSTOM_LLM_BASE_URL is required")
		}
		static.BaseURL = cfg.Custom.BaseURL
		static.APIKey = cfg.Custom.APIKey
		static.Model = cfg.Custom.Model
	default:
		return nil, fmt.Errorf("unsupported provider %q", name)
	}

	return NewFromStatic(static, timeout)
}

// WithRetries оборачивает провайдера повторами для 429/5xx и сетевых ошибок
// с экспоненциальной задержкой (base, 2×base, 4×base…, максимум 30 с).
func WithRetries(p Provider, maxRetries int, baseDelay time.Duration) Provider {
	if maxRetries <= 0 {
		return p
	}
	if baseDelay <= 0 {
		baseDelay = 2 * time.Second
	}
	return &retryProvider{inner: p, maxRetries: maxRetries, baseDelay: baseDelay}
}

type retryProvider struct {
	inner      Provider
	maxRetries int
	baseDelay  time.Duration
}

func (r *retryProvider) Name() string         { return r.inner.Name() }
func (r *retryProvider) DefaultModel() string { return r.inner.DefaultModel() }

func (r *retryProvider) Chat(ctx context.Context, messages []Message, opts ChatOptions) (Response, error) {
	var lastErr error

	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			delay := r.baseDelay << (attempt - 1)
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return Response{}, ctx.Err()
			case <-timer.C:
			}
			log.Printf("[LLM:%s] retry %d/%d after %v: %v", r.inner.Name(), attempt, r.maxRetries, delay, lastErr)
		}

		resp, err := r.inner.Chat(ctx, messages, opts)
		if err == nil {
			return resp, nil
		}
		lastErr = err

		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		if !isRetryable(err) || attempt >= r.maxRetries {
			return Response{}, err
		}
	}
}

func isRetryable(err error) bool {
	if status, ok := StatusCodeOf(err); ok {
		return status == http.StatusTooManyRequests || status >= 500
	}
	// не-HTTP ошибки — сетевые и таймауты, тоже повторяем
	return true
}

// WithFallback строит цепочку провайдеров: при неудаче первичного (включая все retry)
// запрос повторяется на следующем. Если модель не задана явно, каждому провайдеру
// подставляется его собственная модель по умолчанию.
func WithFallback(primary Provider, fallbacks ...Provider) Provider {
	if len(fallbacks) == 0 {
		return primary
	}
	return &fallbackProvider{providers: append([]Provider{primary}, fallbacks...)}
}

type fallbackProvider struct {
	providers []Provider
}

func (f *fallbackProvider) Name() string         { return f.providers[0].Name() }
func (f *fallbackProvider) DefaultModel() string { return f.providers[0].DefaultModel() }

func (f *fallbackProvider) Chat(ctx context.Context, messages []Message, opts ChatOptions) (Response, error) {
	var lastErr error

	for i, p := range f.providers {
		o := opts
		if i > 0 && opts.Model != "" && opts.Model == f.providers[0].DefaultModel() {
			o.Model = p.DefaultModel()
		}

		resp, err := p.Chat(ctx, messages, o)
		if err == nil {
			return resp, nil
		}
		lastErr = err

		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		log.Printf("[LLM] provider %s failed, trying next: %v", p.Name(), err)
	}

	return Response{}, lastErr
}

// WithCounting накапливает фактическое потребление токенов по успешным запросам.
func WithCounting(p Provider) *CountingProvider {
	return &CountingProvider{inner: p}
}

type CountingProvider struct {
	inner Provider

	mu               sync.Mutex
	promptTokens     int64
	completionTokens int64
	totalTokens      int64
	requests         int64
}

func (c *CountingProvider) Name() string         { return c.inner.Name() }
func (c *CountingProvider) DefaultModel() string { return c.inner.DefaultModel() }

func (c *CountingProvider) Chat(ctx context.Context, messages []Message, opts ChatOptions) (Response, error) {
	resp, err := c.inner.Chat(ctx, messages, opts)
	if err == nil {
		c.mu.Lock()
		c.promptTokens += resp.Usage.PromptTokens
		c.completionTokens += resp.Usage.CompletionTokens
		c.totalTokens += resp.Usage.TotalTokens
		c.requests++
		c.mu.Unlock()
	}
	return resp, err
}

// Snapshot возвращает накопленное потребление.
func (c *CountingProvider) Snapshot() Usage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Usage{
		PromptTokens:     c.promptTokens,
		CompletionTokens: c.completionTokens,
		TotalTokens:      c.totalTokens,
	}
}

// Requests возвращает число успешных LLM-вызовов.
func (c *CountingProvider) Requests() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests
}

func newHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 300 * time.Second
	}

	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			IdleConnTimeout:     90 * time.Second,
			TLSHandshakeTimeout: 10 * time.Second,
			Proxy:               http.ProxyFromEnvironment,
		},
	}
}

func truncateString(s string, limit int) string {
	if limit <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}
