package providers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"code-archaeologist/backend/internal/config"
	"code-archaeologist/backend/internal/llm"
	"code-archaeologist/backend/internal/store"
)

var (
	ErrNotFound = errors.New("provider not found")
	ErrConflict = errors.New("provider conflict")
)

var (
	nameRe       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,31}$`)
	headerNameRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
)

// requiresKeyFor — провайдеры, которые не работают без ключа (совпадает с прежним поведением).
func requiresKeyFor(name string) bool {
	switch llm.NormalizeProvider(name) {
	case "openai", "deepseek", "qwen":
		return true
	default:
		return false
	}
}

// Info — провайдер в том виде, в котором его можно отдавать клиенту (без секретов).
type Info struct {
	Name       string            `json:"name"`
	Configured bool              `json:"configured"`
	BaseURL    string            `json:"base_url,omitempty"`
	Model      string            `json:"model,omitempty"`
	APIKeySet  bool              `json:"api_key_set"`
	Custom     bool              `json:"custom"`
	Overridden bool              `json:"overridden"`
	Headers    map[string]string `json:"headers,omitempty"`
}

// UpdateInput — поля обновления. nil = не менять; пустая строка = очистить.
type UpdateInput struct {
	BaseURL *string            `json:"base_url"`
	Model   *string            `json:"model"`
	APIKey  *string            `json:"api_key"`
	Headers *map[string]string `json:"headers"`
}

type resolved struct {
	name        string
	baseURL     string
	apiKey      string
	model       string
	headers     map[string]string
	custom      bool
	overridden  bool
	requiresKey bool
}

// Manager разрешает конфигурацию провайдера: builtin-дефолты < env < настройки из БД.
type Manager struct {
	cfg *config.Config
	st  store.Store
}

func NewManager(cfg *config.Config, st store.Store) *Manager {
	return &Manager{cfg: cfg, st: st}
}

// Knows сообщает, существует ли провайдер (встроенный или созданный пользователем).
func (m *Manager) Knows(name string) bool {
	name = llm.NormalizeProvider(name)
	if llm.IsBuiltin(name) {
		return true
	}
	_, ok := m.st.GetProviderConfig(name)
	return ok
}

// Resolve возвращает эффективную конфигурацию провайдера.
func (m *Manager) Resolve(name string) (llm.StaticConfig, error) {
	name = llm.NormalizeProvider(name)
	row, has := m.st.GetProviderConfig(name)
	res := m.resolve(name, row, has)

	if res.custom && !has {
		return llm.StaticConfig{}, fmt.Errorf("%w: unknown provider %q", ErrNotFound, name)
	}
	if res.baseURL == "" {
		return llm.StaticConfig{}, fmt.Errorf("provider %q is not configured: base URL is empty", name)
	}
	if res.requiresKey && res.apiKey == "" {
		return llm.StaticConfig{}, fmt.Errorf("provider %q is not configured: API key is required", name)
	}

	return llm.StaticConfig{
		Name:    res.name,
		BaseURL: res.baseURL,
		APIKey:  res.apiKey,
		Model:   res.model,
		Headers: res.headers,
	}, nil
}

// Build создаёт готового LLM-провайдера.
func (m *Manager) Build(name string) (llm.Provider, error) {
	sc, err := m.Resolve(name)
	if err != nil {
		return nil, err
	}
	if sc.Model == "" {
		return nil, fmt.Errorf("provider %q is not configured: model is required", name)
	}
	return llm.NewFromStatic(sc, m.cfg.Analysis.RequestTimeout)
}

// Test выполняет минимальный запрос к провайдеру, чтобы проверить соединение и ключ.
func (m *Manager) Test(ctx context.Context, name string) (string, error) {
	p, err := m.Build(name)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	return p.Chat(ctx, []llm.Message{{Role: "user", Content: "Reply with a single word: ok"}}, llm.ChatOptions{
		Temperature: 0,
		MaxTokens:   10,
	})
}

// List возвращает провайдеров для UI: встроенные + пользовательские, без секретов.
func (m *Manager) List() ([]Info, error) {
	rows, err := m.st.ListProviderConfigs()
	if err != nil {
		return nil, err
	}

	byName := make(map[string]store.ProviderConfig, len(rows))
	for _, r := range rows {
		byName[r.Name] = r
	}

	out := make([]Info, 0, len(llm.BuiltinProviders())+len(rows))
	for _, name := range llm.BuiltinProviders() {
		row, has := byName[name]
		out = append(out, m.infoFor(name, row, has))
	}

	customs := make([]string, 0, len(rows))
	for name := range byName {
		if !llm.IsBuiltin(name) {
			customs = append(customs, name)
		}
	}
	sort.Strings(customs)
	for _, name := range customs {
		row := byName[name]
		out = append(out, m.infoFor(name, row, true))
	}

	return out, nil
}

// Update сохраняет настройки существующего провайдера.
func (m *Manager) Update(name string, in UpdateInput) (Info, error) {
	name = llm.NormalizeProvider(name)
	if !nameRe.MatchString(name) {
		return Info{}, fmt.Errorf("invalid provider name %q", name)
	}

	row, has := m.st.GetProviderConfig(name)
	if !has {
		if !llm.IsBuiltin(name) {
			return Info{}, fmt.Errorf("%w: unknown provider %q", ErrNotFound, name)
		}
		res := m.resolve(name, nil, false)
		row = &store.ProviderConfig{
			Name:    name,
			BaseURL: res.baseURL,
			APIKey:  res.apiKey,
			Model:   res.model,
		}
	}

	if in.BaseURL != nil {
		row.BaseURL = strings.TrimSpace(*in.BaseURL)
	}
	if in.Model != nil {
		row.Model = strings.TrimSpace(*in.Model)
	}
	if in.APIKey != nil {
		row.APIKey = strings.TrimSpace(*in.APIKey)
	}
	if in.Headers != nil {
		row.Headers = *in.Headers
	}

	if err := validate(*row); err != nil {
		return Info{}, err
	}
	if err := m.st.SaveProviderConfig(*row); err != nil {
		return Info{}, err
	}

	return m.infoFor(name, *row, true), nil
}

// Create добавляет пользовательского провайдера (имя не должно совпадать со встроенными).
func (m *Manager) Create(name string, in UpdateInput) (Info, error) {
	name = llm.NormalizeProvider(name)
	if !nameRe.MatchString(name) {
		return Info{}, fmt.Errorf("provider name must match %q", nameRe.String())
	}
	if llm.IsBuiltin(name) {
		return Info{}, fmt.Errorf("%w: provider %q is built-in", ErrConflict, name)
	}
	if _, exists := m.st.GetProviderConfig(name); exists {
		return Info{}, fmt.Errorf("%w: provider %q already exists", ErrConflict, name)
	}

	row := store.ProviderConfig{Name: name}
	if in.BaseURL != nil {
		row.BaseURL = strings.TrimSpace(*in.BaseURL)
	}
	if in.Model != nil {
		row.Model = strings.TrimSpace(*in.Model)
	}
	if in.APIKey != nil {
		row.APIKey = strings.TrimSpace(*in.APIKey)
	}
	if in.Headers != nil {
		row.Headers = *in.Headers
	}

	if err := validate(row); err != nil {
		return Info{}, err
	}
	if err := m.st.SaveProviderConfig(row); err != nil {
		return Info{}, err
	}

	return m.infoFor(name, row, true), nil
}

// Delete удаляет настройки: для встроенного — сбрасывает на env/дефолт, для пользовательского — удаляет его.
func (m *Manager) Delete(name string) error {
	name = llm.NormalizeProvider(name)
	if _, ok := m.st.GetProviderConfig(name); !ok {
		return fmt.Errorf("%w: provider %q has no saved settings", ErrNotFound, name)
	}
	return m.st.DeleteProviderConfig(name)
}

func (m *Manager) resolve(name string, row *store.ProviderConfig, has bool) resolved {
	name = llm.NormalizeProvider(name)
	custom := !llm.IsBuiltin(name)
	requiresKey := requiresKeyFor(name)

	var baseURL, apiKey, model string
	if !custom {
		switch name {
		case "ollama":
			baseURL, model = m.cfg.Ollama.BaseURL, m.cfg.Ollama.Model
		case "llamacpp":
			baseURL, apiKey, model = m.cfg.LlamaCpp.BaseURL, m.cfg.LlamaCpp.APIKey, m.cfg.LlamaCpp.Model
		case "openai":
			baseURL, apiKey, model = m.cfg.OpenAI.BaseURL, m.cfg.OpenAI.APIKey, m.cfg.OpenAI.Model
		case "deepseek":
			baseURL, apiKey, model = m.cfg.DeepSeek.BaseURL, m.cfg.DeepSeek.APIKey, m.cfg.DeepSeek.Model
		case "qwen":
			baseURL, apiKey, model = m.cfg.Qwen.BaseURL, m.cfg.Qwen.APIKey, m.cfg.Qwen.Model
		case "custom":
			baseURL, apiKey, model = m.cfg.Custom.BaseURL, m.cfg.Custom.APIKey, m.cfg.Custom.Model
		}
	}

	// Настройки из БД — самодостаточный снимок, полностью перекрывающий env.
	if has && row != nil {
		baseURL, apiKey, model = row.BaseURL, row.APIKey, row.Model
	}

	return resolved{
		name:        name,
		baseURL:     baseURL,
		apiKey:      apiKey,
		model:       model,
		headers:     providerHeaders(row, has),
		custom:      custom,
		overridden:  has && row != nil,
		requiresKey: requiresKey,
	}
}

func providerHeaders(row *store.ProviderConfig, has bool) map[string]string {
	if !has || row == nil || len(row.Headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(row.Headers))
	for k, v := range row.Headers {
		out[k] = v
	}
	return out
}

func (m *Manager) infoFor(name string, row store.ProviderConfig, has bool) Info {
	res := m.resolve(name, &row, has)
	info := Info{
		Name:       res.name,
		Configured: res.baseURL != "" && (!res.requiresKey || res.apiKey != ""),
		BaseURL:    res.baseURL,
		Model:      res.model,
		APIKeySet:  res.apiKey != "",
		Custom:     res.custom,
		Overridden: res.overridden,
	}
	if len(res.headers) > 0 {
		info.Headers = maskHeaders(res.headers, res.apiKey)
	}
	return info
}

// maskHeaders скрывает реальный ключ, если он вписан в заголовок явно.
func maskHeaders(headers map[string]string, apiKey string) map[string]string {
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		if apiKey != "" {
			v = strings.ReplaceAll(v, apiKey, llm.APITokenPlaceholder)
		}
		out[k] = v
	}
	return out
}

func validate(pc store.ProviderConfig) error {
	if pc.BaseURL == "" {
		return fmt.Errorf("base URL is required")
	}
	if len(pc.BaseURL) > 2048 {
		return fmt.Errorf("base URL is too long")
	}
	u, err := url.Parse(pc.BaseURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("base URL must be a valid http(s) URL")
	}
	if len(pc.Model) > 200 {
		return fmt.Errorf("model is too long")
	}
	if len(pc.APIKey) > 512 {
		return fmt.Errorf("API key is too long")
	}
	if len(pc.Headers) > 32 {
		return fmt.Errorf("too many headers")
	}
	for k, v := range pc.Headers {
		if !headerNameRe.MatchString(k) {
			return fmt.Errorf("invalid header name %q", k)
		}
		if v == "" {
			return fmt.Errorf("header %q has empty value", k)
		}
		if len(v) > 1024 {
			return fmt.Errorf("header %q is too long", k)
		}
		for i := 0; i < len(v); i++ {
			if v[i] < 0x20 && v[i] != '\t' {
				return fmt.Errorf("header %q contains invalid characters", k)
			}
		}
	}
	return nil
}
