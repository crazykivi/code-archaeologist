package llm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
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

type Provider interface {
	Name() string
	DefaultModel() string
	Chat(ctx context.Context, messages []Message, opts ChatOptions) (string, error)
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
