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

type openAIProvider struct {
	name         string
	endpoint     string
	apiKey       string
	defaultModel string
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
}

func NewOpenAICompatible(name, baseURL, apiKey, model string, timeout time.Duration) (*openAIProvider, error) {
	endpoint := buildOpenAIEndpoint(baseURL)
	if endpoint == "" {
		return nil, fmt.Errorf("%s base URL is empty", name)
	}

	if _, err := url.ParseRequestURI(endpoint); err != nil {
		return nil, fmt.Errorf("%s base URL is invalid: %w", name, err)
	}

	return &openAIProvider{
		name:         name,
		endpoint:     endpoint,
		apiKey:       apiKey,
		defaultModel: model,
		client:       newHTTPClient(timeout),
	}, nil
}

func (p *openAIProvider) Name() string {
	return p.name
}

func (p *openAIProvider) DefaultModel() string {
	return p.defaultModel
}

func (p *openAIProvider) Chat(ctx context.Context, messages []Message, opts ChatOptions) (string, error) {
	model := opts.Model
	if model == "" {
		model = p.defaultModel
	}
	if model == "" {
		return "", fmt.Errorf("%s model is required", p.name)
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
		return "", fmt.Errorf("%s failed to encode request: %w", p.name, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("%s failed to create request: %w", p.name, err)
	}

	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s request failed: %w", p.name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("%s provider returned HTTP %d: %s", p.name, resp.StatusCode, truncateString(string(errBody), 512))
	}

	var out openAIResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("%s invalid response: %w", p.name, err)
	}

	if len(out.Choices) == 0 {
		return "", fmt.Errorf("%s returned no choices", p.name)
	}

	return strings.TrimSpace(out.Choices[0].Message.Content), nil
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
