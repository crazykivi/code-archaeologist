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

type ollamaProvider struct {
	endpoint     string
	defaultModel string
	client       *http.Client
}

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaOptions struct {
	Temperature float64 `json:"temperature,omitempty"`
	NumPredict  int     `json:"num_predict,omitempty"`
}

type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
	Options  *ollamaOptions  `json:"options,omitempty"`
}

type ollamaResponse struct {
	Message ollamaMessage `json:"message"`
	Error   string        `json:"error"`
}

func NewOllama(baseURL, model string, timeout time.Duration) (*ollamaProvider, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, fmt.Errorf("ollama base URL is empty")
	}

	endpoint := strings.TrimRight(baseURL, "/") + "/api/chat"
	if _, err := url.ParseRequestURI(endpoint); err != nil {
		return nil, fmt.Errorf("ollama base URL is invalid: %w", err)
	}

	return &ollamaProvider{
		endpoint:     endpoint,
		defaultModel: model,
		client:       newHTTPClient(timeout),
	}, nil
}

func (p *ollamaProvider) Name() string {
	return "ollama"
}

func (p *ollamaProvider) DefaultModel() string {
	return p.defaultModel
}

func (p *ollamaProvider) Chat(ctx context.Context, messages []Message, opts ChatOptions) (string, error) {
	model := opts.Model
	if model == "" {
		model = p.defaultModel
	}
	if model == "" {
		return "", fmt.Errorf("ollama model is required")
	}

	reqMessages := make([]ollamaMessage, 0, len(messages))
	for _, m := range messages {
		reqMessages = append(reqMessages, ollamaMessage{
			Role:    m.Role,
			Content: m.Content,
		})
	}

	payload := ollamaRequest{
		Model:    model,
		Messages: reqMessages,
		Stream:   false,
		Options: &ollamaOptions{
			Temperature: opts.Temperature,
			NumPredict:  opts.MaxTokens,
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("ollama failed to encode request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ollama failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", fmt.Errorf("ollama provider returned HTTP %d: %s", resp.StatusCode, truncateString(string(errBody), 512))
	}

	var out ollamaResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return "", fmt.Errorf("ollama invalid response: %w", err)
	}

	if out.Error != "" {
		return "", fmt.Errorf("ollama error: %s", truncateString(out.Error, 512))
	}

	return strings.TrimSpace(out.Message.Content), nil
}
