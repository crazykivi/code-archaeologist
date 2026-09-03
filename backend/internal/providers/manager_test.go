package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"code-archaeologist/backend/internal/config"
	"code-archaeologist/backend/internal/llm"
	"code-archaeologist/backend/internal/store"
)

func testConfig() *config.Config {
	return &config.Config{
		Analysis: config.AnalysisConfig{RequestTimeout: 5_000_000_000},
		Ollama:   config.OllamaConfig{BaseURL: "http://127.0.0.1:11434", Model: "llama3.1:8b"},
		OpenAI: config.ProviderEndpointConfig{
			BaseURL: "https://api.openai.com/v1",
			Model:   "gpt-4o-mini",
		},
		Custom: config.ProviderEndpointConfig{BaseURL: "https://api.b.ai/v1", Model: "glm-5.3-flash"},
	}
}

func TestResolveDefaults(t *testing.T) {
	m := NewManager(testConfig(), store.NewMemoryStore())

	sc, err := m.Resolve("ollama")
	if err != nil {
		t.Fatalf("resolve ollama: %v", err)
	}
	if sc.BaseURL != "http://127.0.0.1:11434" || sc.Model != "llama3.1:8b" {
		t.Errorf("unexpected ollama config: %+v", sc)
	}

	// openai без ключа — не конфигурирован
	if _, err := m.Resolve("openai"); err == nil {
		t.Error("expected error for openai without API key")
	}

	// builtin custom из env
	sc, err = m.Resolve("custom")
	if err != nil {
		t.Fatalf("resolve custom: %v", err)
	}
	if sc.BaseURL != "https://api.b.ai/v1" {
		t.Errorf("unexpected custom base url: %s", sc.BaseURL)
	}

	if _, err := m.Resolve("no-such"); err == nil {
		t.Error("expected error for unknown provider")
	}
}

func strPtr(s string) *string { return &s }

func TestUpdateSnapshotAndMasking(t *testing.T) {
	m := NewManager(testConfig(), store.NewMemoryStore())

	headers := map[string]string{"Authorization": "Bearer {{api_key}}", "X-Custom": "abc"}
	info, err := m.Update("openai", UpdateInput{
		BaseURL: strPtr("https://api.b.ai/v1"),
		Model:   strPtr("glm-5.3-flash"),
		APIKey:  strPtr("sk-test-123"),
		Headers: &headers,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !info.Overridden || !info.APIKeySet || !info.Configured {
		t.Errorf("unexpected info: %+v", info)
	}

	// ключ не возвращается, заголовок замаскирован
	if info.Headers["Authorization"] != "Bearer {{api_key}}" {
		t.Errorf("masked header: %q", info.Headers["Authorization"])
	}

	sc, err := m.Resolve("openai")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sc.APIKey != "sk-test-123" || sc.Headers["X-Custom"] != "abc" {
		t.Errorf("unexpected resolved config: %+v", sc)
	}

	// api_key: nil — ключ сохраняется
	if _, err := m.Update("openai", UpdateInput{Model: strPtr("glm-6")}); err != nil {
		t.Fatalf("update without key: %v", err)
	}
	sc, _ = m.Resolve("openai")
	if sc.APIKey != "sk-test-123" {
		t.Errorf("key must be preserved, got %q", sc.APIKey)
	}

	// api_key: "" — ключ очищается
	if _, err := m.Update("openai", UpdateInput{APIKey: strPtr("")}); err != nil {
		t.Fatalf("clear key: %v", err)
	}
	sc, _ = m.Resolve("openai")
	if sc.APIKey != "" {
		t.Errorf("key must be cleared, got %q", sc.APIKey)
	}

	// удаление override возвращает env-конфигурацию
	if err := m.Delete("openai"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := m.Resolve("openai"); err == nil {
		t.Error("expected error after reverting to env without key")
	}
}

func TestCreateCustomProvider(t *testing.T) {
	m := NewManager(testConfig(), store.NewMemoryStore())

	// имя нормализуется в нижний регистр и принимается
	proxy, err := m.Create("OpenAI-Proxy", UpdateInput{BaseURL: strPtr("https://api.b.ai/v1")})
	if err != nil {
		t.Fatalf("create normalized name: %v", err)
	}
	if proxy.Name != "openai-proxy" {
		t.Errorf("name must be normalized, got %q", proxy.Name)
	}
	if err := m.Delete("openai-proxy"); err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	if _, err := m.Create("bad_name", UpdateInput{BaseURL: strPtr("https://x/v1")}); err == nil {
		t.Error("expected validation error for underscore in name")
	}

	info, err := m.Create("b-ai", UpdateInput{
		BaseURL: strPtr("https://api.b.ai/v1"),
		Model:   strPtr("glm-5.3-flash"),
		APIKey:  strPtr("key-42"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !info.Custom || !info.Configured {
		t.Errorf("unexpected info: %+v", info)
	}

	if _, err := m.Create("b-ai", UpdateInput{BaseURL: strPtr("https://x/v1")}); err == nil {
		t.Error("expected conflict for duplicate")
	}
	if _, err := m.Create("openai", UpdateInput{BaseURL: strPtr("https://x/v1")}); err == nil {
		t.Error("expected conflict for builtin name")
	}

	if !m.Knows("b-ai") {
		t.Error("custom provider must be known")
	}
	if err := m.Delete("b-ai"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if m.Knows("b-ai") {
		t.Error("custom provider must be forgotten after delete")
	}
}

func TestValidationErrors(t *testing.T) {
	m := NewManager(testConfig(), store.NewMemoryStore())

	cases := []struct {
		name string
		in   UpdateInput
	}{
		{"bad scheme", UpdateInput{BaseURL: strPtr("ftp://x"), Model: strPtr("m")}},
		{"no host", UpdateInput{BaseURL: strPtr("http://"), Model: strPtr("m")}},
		{"header name bad", UpdateInput{BaseURL: strPtr("http://x"), Model: strPtr("m"),
			Headers: mapPtr(map[string]string{"Bad Header!": "v"})}},
		{"header CRLF", UpdateInput{BaseURL: strPtr("http://x"), Model: strPtr("m"),
			Headers: mapPtr(map[string]string{"X-A": "v1\r\nX-B: v2"})}},
	}

	for _, c := range cases {
		if _, err := m.Create("myprov", c.in); err == nil {
			t.Errorf("%s: expected error", c.name)
		}
	}
}

func mapPtr(m map[string]string) *map[string]string { return &m }

func TestBuildRequestHeaders(t *testing.T) {
	var gotAuth, gotCustom string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotCustom = r.Header.Get("X-Custom")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": "ok"}},
			},
		})
	}))
	defer srv.Close()

	m := NewManager(testConfig(), store.NewMemoryStore())

	headers := map[string]string{
		"Authorization": "Bearer {{api_key}}",
		"X-Custom":      "custom-value",
	}
	if _, err := m.Update("openai", UpdateInput{
		BaseURL: strPtr(srv.URL + "/v1"),
		Model:   strPtr("test-model"),
		APIKey:  strPtr("sk-live-999"),
		Headers: &headers,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	provider, err := m.Build("openai")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, err := provider.Chat(context.Background(), []llm.Message{{Role: "user", Content: "ping"}}, llm.ChatOptions{}); err != nil {
		t.Fatalf("chat: %v", err)
	}

	if gotAuth != "Bearer sk-live-999" {
		t.Errorf("authorization header: %q", gotAuth)
	}
	if gotCustom != "custom-value" {
		t.Errorf("custom header: %q", gotCustom)
	}
}

func TestBuildCustomAuthScheme(t *testing.T) {
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()

	m := NewManager(testConfig(), store.NewMemoryStore())

	// Провайдер требует сырой ключ без Bearer — задаём свой заголовок.
	headers := map[string]string{"Authorization": "{{api_key}}"}
	if _, err := m.Update("openai", UpdateInput{
		BaseURL: strPtr(srv.URL + "/v1"),
		Model:   strPtr("test-model"),
		APIKey:  strPtr("raw-key"),
		Headers: &headers,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}

	provider, err := m.Build("openai")
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, err := provider.Chat(context.Background(), []llm.Message{{Role: "user", Content: "ping"}}, llm.ChatOptions{}); err != nil {
		t.Fatalf("chat: %v", err)
	}

	if gotAuth != "raw-key" {
		t.Errorf("authorization header: %q", gotAuth)
	}
	if strings.Contains(gotAuth, "Bearer") {
		t.Error("raw scheme must not be overridden by Bearer")
	}
}
