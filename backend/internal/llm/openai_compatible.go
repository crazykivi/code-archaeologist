package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// APITokenPlaceholder в значении кастомного заголовка заменяется на реальный API-ключ.
const APITokenPlaceholder = "{{api_key}}"

// StaticConfig — полный набор параметров провайдера после разрешения дефолтов/env/настроек из БД.
type StaticConfig struct {
	Name    string
	BaseURL string
	APIKey  string
	Model   string
	Headers map[string]string
}

type openAIProvider struct {
	name         string
	endpoint     string
	apiKey       string
	defaultModel string
	headers      map[string]string
	client       *http.Client
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float64         `json:"temperature"`
	Stream      bool            `json:"stream"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
}

type openAIResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
	Usage usage `json:"usage"`
}

type usage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

func NewOpenAICompatible(cfg StaticConfig, timeout time.Duration) (*openAIProvider, error) {
	endpoint := buildOpenAIEndpoint(cfg.BaseURL)
	if endpoint == "" {
		return nil, fmt.Errorf("%s base URL is empty", cfg.Name)
	}

	if _, err := url.ParseRequestURI(endpoint); err != nil {
		return nil, fmt.Errorf("%s base URL is invalid: %w", cfg.Name, err)
	}

	return &openAIProvider{
		name:         cfg.Name,
		endpoint:     endpoint,
		apiKey:       cfg.APIKey,
		defaultModel: cfg.Model,
		headers:      cfg.Headers,
		client:       newHTTPClient(timeout),
	}, nil
}

func (p *openAIProvider) Name() string {
	return p.name
}

func (p *openAIProvider) DefaultModel() string {
	return p.defaultModel
}

func (p *openAIProvider) Chat(ctx context.Context, messages []Message, opts ChatOptions) (Response, error) {
	model := opts.Model
	if model == "" {
		model = p.defaultModel
	}
	if model == "" {
		return Response{}, fmt.Errorf("%s model is required", p.name)
	}

	reqMessages := make([]openAIMessage, 0, len(messages))
	for _, m := range messages {
		reqMessages = append(reqMessages, openAIMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	payload := openAIRequest{
		Model:       model,
		Messages:    reqMessages,
		Temperature: opts.Temperature,
		Stream:      false,
		MaxTokens:   opts.MaxTokens,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return Response{}, fmt.Errorf("%s failed to encode request: %w", p.name, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return Response{}, fmt.Errorf("%s failed to create request: %w", p.name, err)
	}

	req.Header.Set("Content-Type", "application/json")
	applyHeaders(req, p.headers, p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("%s request failed: %w", p.name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return Response{}, &HTTPError{
			Provider: p.name,
			Status:   resp.StatusCode,
			Body:     string(errBody),
		}
	}

	var out openAIResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return Response{}, fmt.Errorf("%s invalid response: %w", p.name, err)
	}

	if len(out.Choices) == 0 {
		return Response{}, fmt.Errorf("%s returned no choices", p.name)
	}

	return Response{
		Content: strings.TrimSpace(out.Choices[0].Message.Content),
		Usage: Usage{
			PromptTokens:     out.Usage.PromptTokens,
			CompletionTokens: out.Usage.CompletionTokens,
			TotalTokens:      out.Usage.TotalTokens,
		},
	}, nil
}

// applyHeaders применяет кастомные заголовки с подстановкой ключа,
// а при их отсутствии — стандартный Authorization: Bearer.
func applyHeaders(req *http.Request, headers map[string]string, apiKey string) {
	hasAuth := false
	for k, v := range headers {
		if strings.EqualFold(k, "Authorization") {
			hasAuth = true
		}
		req.Header.Set(k, strings.ReplaceAll(v, APITokenPlaceholder, apiKey))
	}
	if !hasAuth && apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
}

func buildOpenAIEndpoint(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return ""
	}

	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, "/chat/completions") {
		return baseURL
	}
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + "/chat/completions"
	}
	return baseURL + "/v1/chat/completions"
}
