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

func IsSupported(name string) bool {
	switch NormalizeProvider(name) {
	case "ollama", "openai", "deepseek", "qwen", "llamacpp", "custom":
		return true
	default:
		return false
	}
}

func NewProvider(name string, cfg *config.Config) (Provider, error) {
	name = NormalizeProvider(name)
	timeout := cfg.Analysis.RequestTimeout

	switch name {
	case "ollama":
		return NewOllama(cfg.Ollama.BaseURL, cfg.Ollama.Model, timeout)

	case "llamacpp":
		return NewOpenAICompatible("llamacpp", cfg.LlamaCpp.BaseURL, cfg.LlamaCpp.APIKey, cfg.LlamaCpp.Model, timeout)

	case "openai":
		if cfg.OpenAI.APIKey == "" {
			return nil, fmt.Errorf("OPENAI_API_KEY is required")
		}
		return NewOpenAICompatible("openai", cfg.OpenAI.BaseURL, cfg.OpenAI.APIKey, cfg.OpenAI.Model, timeout)

	case "deepseek":
		if cfg.DeepSeek.APIKey == "" {
			return nil, fmt.Errorf("DEEPSEEK_API_KEY is required")
		}
		return NewOpenAICompatible("deepseek", cfg.DeepSeek.BaseURL, cfg.DeepSeek.APIKey, cfg.DeepSeek.Model, timeout)

	case "qwen":
		if cfg.Qwen.APIKey == "" {
			return nil, fmt.Errorf("DASHSCOPE_API_KEY is required")
		}
		return NewOpenAICompatible("qwen", cfg.Qwen.BaseURL, cfg.Qwen.APIKey, cfg.Qwen.Model, timeout)

	case "custom":
		if cfg.Custom.BaseURL == "" {
			return nil, fmt.Errorf("CUSTOM_LLM_BASE_URL is required")
		}
		return NewOpenAICompatible("custom", cfg.Custom.BaseURL, cfg.Custom.APIKey, cfg.Custom.Model, timeout)

	default:
		return nil, fmt.Errorf("unsupported provider %q", name)
	}
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
