package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

type fakeProvider struct {
	name  string
	model string

	calls    atomic.Int64
	failN    int64 // сколько первых вызовов вернуть ошибкой
	err      error
	response string
	usage    Usage

	seenModels []string
}

func (f *fakeProvider) Name() string         { return f.name }
func (f *fakeProvider) DefaultModel() string { return f.model }

func (f *fakeProvider) Chat(ctx context.Context, messages []Message, opts ChatOptions) (Response, error) {
	n := f.calls.Add(1)
	f.seenModels = append(f.seenModels, opts.Model)
	if f.failN > 0 && n <= f.failN {
		return Response{}, f.err
	}
	return Response{Content: f.response, Usage: f.usage}, nil
}

func TestWithRetriesRetries5xx(t *testing.T) {
	fp := &fakeProvider{
		name:     "test",
		model:    "m",
		response: "ok",
		failN:    2,
		err:      &HTTPError{Provider: "test", Status: 500, Body: "boom"},
	}
	p := WithRetries(fp, 3, time.Millisecond)

	resp, err := p.Chat(context.Background(), nil, ChatOptions{Model: "m"})
	if err != nil {
		t.Fatalf("expected success after retries: %v", err)
	}
	if resp.Content != "ok" {
		t.Errorf("unexpected content: %q", resp.Content)
	}
	if fp.calls.Load() != 3 {
		t.Errorf("expected 3 attempts, got %d", fp.calls.Load())
	}
}

func TestWithRetriesDoesNotRetry4xx(t *testing.T) {
	fp := &fakeProvider{
		name:  "test",
		model: "m",
		failN: 99,
		err:   &HTTPError{Provider: "test", Status: 401, Body: "unauthorized"},
	}
	p := WithRetries(fp, 3, time.Millisecond)

	if _, err := p.Chat(context.Background(), nil, ChatOptions{Model: "m"}); err == nil {
		t.Fatal("expected error")
	}
	if fp.calls.Load() != 1 {
		t.Errorf("4xx must not be retried, got %d calls", fp.calls.Load())
	}
}

func TestWithFallback(t *testing.T) {
	primary := &fakeProvider{
		name:  "primary",
		model: "primary-model",
		failN: 99,
		err:   &HTTPError{Provider: "primary", Status: 503, Body: "down"},
	}
	secondary := &fakeProvider{name: "secondary", model: "secondary-model", response: "from-secondary"}

	p := WithRetries(primary, 1, time.Millisecond)
	fs := WithRetries(secondary, 1, time.Millisecond)
	chain := WithFallback(p, fs)

	resp, err := chain.Chat(context.Background(), nil, ChatOptions{Model: "primary-model"})
	if err != nil {
		t.Fatalf("expected fallback success: %v", err)
	}
	if resp.Content != "from-secondary" {
		t.Errorf("unexpected content: %q", resp.Content)
	}
	// Ключевое: fallback-провайдеру ушла ЕГО модель, а не модель primary.
	if secondary.seenModels[0] != "secondary-model" {
		t.Errorf("model substitution failed: %q", secondary.seenModels[0])
	}
}

func TestWithCounting(t *testing.T) {
	fp := &fakeProvider{name: "test", model: "m", response: "ok", usage: Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}}
	p := WithCounting(WithRetries(fp, 2, time.Millisecond))
	fp.failN = 1
	fp.err = &HTTPError{Provider: "test", Status: 429, Body: "rate"}

	if _, err := p.Chat(context.Background(), nil, ChatOptions{Model: "m"}); err != nil {
		t.Fatalf("chat: %v", err)
	}

	// 429 был ретраем и не должен считаться (только успешные вызовы).
	u := p.Snapshot()
	if u.TotalTokens != 15 || u.PromptTokens != 10 {
		t.Errorf("unexpected usage: %+v", u)
	}
	if p.Requests() != 1 {
		t.Errorf("unexpected requests: %d", p.Requests())
	}
}

func TestProviderChatWithUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`))
	}))
	defer srv.Close()

	p, err := NewFromStatic(StaticConfig{Name: "test", BaseURL: srv.URL + "/v1", APIKey: "k", Model: "m"}, time.Second)
	if err != nil {
		t.Fatalf("new provider: %v", err)
	}

	resp, err := p.Chat(context.Background(), []Message{{Role: "user", Content: "hello"}}, ChatOptions{})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}
	if resp.Content != "hi" {
		t.Errorf("content: %q", resp.Content)
	}
	if resp.Usage.TotalTokens != 10 || resp.Usage.PromptTokens != 7 || resp.Usage.CompletionTokens != 3 {
		t.Errorf("usage: %+v", resp.Usage)
	}
}

func TestStatusCodeOf(t *testing.T) {
	if _, ok := StatusCodeOf(errors.New("network")); ok {
		t.Error("plain error must not report status")
	}
	status, ok := StatusCodeOf(&HTTPError{Status: 429})
	if !ok || status != 429 {
		t.Errorf("unexpected: %d %v", status, ok)
	}
}
