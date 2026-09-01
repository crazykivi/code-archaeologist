package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type OllamaConfig struct {
	BaseURL string
	Model   string
}

type ProviderEndpointConfig struct {
	BaseURL string
	APIKey  string
	Model   string
}

type GitConfig struct {
	AllowedHosts []string
	LocalRoots   []string
	CloneRoot    string
	Timeout      time.Duration
	MaxCommits   int
}

type AnalysisConfig struct {
	BatchSize         int
	MaxConcurrentJobs int
	RequestTimeout    time.Duration
	JobTimeout        time.Duration
	Temperature       float64
}

type CascadeConfig struct {
	Enabled     bool
	MaxParallel int
	ReduceSize  int
}

type DiffConfig struct {
	Enabled          bool
	MaxSizePerCommit int
}

type Config struct {
	Addr           string
	AppEnv         string
	Production     bool
	GinMode        string
	AllowOrigins   []string
	TrustedProxies []string

	DefaultProvider string
	DBPath          string

	Ollama   OllamaConfig
	OpenAI   ProviderEndpointConfig
	DeepSeek ProviderEndpointConfig
	Qwen     ProviderEndpointConfig
	LlamaCpp ProviderEndpointConfig
	Custom   ProviderEndpointConfig

	Git      GitConfig
	Analysis AnalysisConfig
	Cascade  CascadeConfig
	Diff     DiffConfig

	RateLimitAnalyzePerMinute int
	RateLimitReadPerMinute    int
}

func Load() (*Config, error) {
	if !strings.EqualFold(os.Getenv("APP_ENV"), "production") {
		for _, p := range []string{".env", "backend/.env"} {
			if loadDotEnv(p) {
				break
			}
		}
	}

	appEnv := getEnv("APP_ENV", "development")
	production := strings.EqualFold(appEnv, "production")

	ginMode := getEnv("GIN_MODE", "")
	if ginMode == "" {
		if production {
			ginMode = "release"
		} else {
			ginMode = "debug"
		}
	}

	addr := getEnv("ADDR", "")
	if addr == "" {
		addr = ":" + getEnv("PORT", "8080")
	}

	defaultProvider := strings.ToLower(strings.TrimSpace(getEnv("DEFAULT_LLM_PROVIDER", "ollama")))
	switch defaultProvider {
	case "ollama", "openai", "gpt", "deepseek", "qwen", "llamacpp", "llama.cpp", "llama-cpp", "custom":
	default:
		return nil, fmt.Errorf("DEFAULT_LLM_PROVIDER %q is not supported", defaultProvider)
	}

	temperature := getEnvFloat("LLM_TEMPERATURE", 0.1)
	if temperature < 0 {
		temperature = 0
	}
	if temperature > 2 {
		temperature = 2
	}

	cfg := &Config{
		Addr:            addr,
		AppEnv:          appEnv,
		Production:      production,
		GinMode:         ginMode,
		AllowOrigins:    getEnvList("ALLOW_CORS", nil),
		TrustedProxies:  getEnvList("TRUSTED_PROXIES", nil),
		DefaultProvider: defaultProvider,
		DBPath:          getEnv("DB_PATH", "data.db"),

		Ollama: OllamaConfig{
			BaseURL: getEnv("OLLAMA_BASE_URL", "http://127.0.0.1:11434"),
			Model:   getEnv("OLLAMA_MODEL", "llama3.1:8b"),
		},
		OpenAI: ProviderEndpointConfig{
			BaseURL: getEnv("OPENAI_BASE_URL", "https://api.openai.com/v1"),
			APIKey:  getEnv("OPENAI_API_KEY", ""),
			Model:   getEnv("OPENAI_MODEL", "gpt-4o-mini"),
		},
		DeepSeek: ProviderEndpointConfig{
			BaseURL: getEnv("DEEPSEEK_BASE_URL", "https://api.deepseek.com/v1"),
			APIKey:  getEnv("DEEPSEEK_API_KEY", ""),
			Model:   getEnv("DEEPSEEK_MODEL", "deepseek-chat"),
		},
		Qwen: ProviderEndpointConfig{
			BaseURL: getEnv("QWEN_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1"),
			APIKey:  getEnv("DASHSCOPE_API_KEY", ""),
			Model:   getEnv("QWEN_MODEL", "qwen-plus"),
		},
		LlamaCpp: ProviderEndpointConfig{
			BaseURL: getEnv("LLAMACPP_BASE_URL", "http://127.0.0.1:8080/v1"),
			APIKey:  getEnv("LLAMACPP_API_KEY", ""),
			Model:   getEnv("LLAMACPP_MODEL", "local"),
		},
		Custom: ProviderEndpointConfig{
			BaseURL: getEnv("CUSTOM_LLM_BASE_URL", ""),
			APIKey:  getEnv("CUSTOM_LLM_API_KEY", ""),
			Model:   getEnv("CUSTOM_LLM_MODEL", ""),
		},

		Git: GitConfig{
			AllowedHosts: getEnvList("GIT_ALLOWED_HOSTS", nil),
			LocalRoots:   getEnvList("LOCAL_ROOTS", nil),
			CloneRoot:    getEnv("CLONE_ROOT", ""),
			Timeout:      time.Duration(getEnvInt("GIT_TIMEOUT_SECONDS", 600)) * time.Second,
			MaxCommits:   getEnvInt("MAX_COMMITS", 10000),
		},

		Analysis: AnalysisConfig{
			BatchSize:         getEnvInt("ANALYSIS_BATCH_SIZE", 20),
			MaxConcurrentJobs: getEnvInt("MAX_CONCURRENT_JOBS", 1),
			RequestTimeout:    time.Duration(getEnvInt("LLM_REQUEST_TIMEOUT_SECONDS", 1800)) * time.Second,
			JobTimeout:        time.Duration(getEnvInt("JOB_TIMEOUT_SECONDS", 7200)) * time.Second,
			Temperature:       temperature,
		},

		Cascade: CascadeConfig{
			Enabled:     getEnvBool("CASCADE_ENABLED", true),
			MaxParallel: getEnvInt("CASCADE_MAX_PARALLEL", 1),
			ReduceSize:  getEnvInt("CASCADE_REDUCE_SIZE", 50),
		},

		Diff: DiffConfig{
			Enabled:          getEnvBool("DIFF_ENABLED", true),
			MaxSizePerCommit: getEnvInt("DIFF_MAX_SIZE_PER_COMMIT", 3000),
		},

		RateLimitAnalyzePerMinute: getEnvInt("RATE_LIMIT_ANALYZE_PER_MINUTE", 5),
		RateLimitReadPerMinute:    getEnvInt("RATE_LIMIT_READ_PER_MINUTE", 120),
	}

	allowedHosts := make([]string, 0, len(cfg.Git.AllowedHosts))
	for _, h := range cfg.Git.AllowedHosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" {
			allowedHosts = append(allowedHosts, h)
		}
	}
	if len(allowedHosts) == 0 {
		allowedHosts = []string{"github.com", "gitlab.com"}
	}
	cfg.Git.AllowedHosts = allowedHosts

	localRoots := make([]string, 0, len(cfg.Git.LocalRoots))
	for _, root := range cfg.Git.LocalRoots {
		abs, err := expandPath(root)
		if err != nil {
			return nil, fmt.Errorf("invalid LOCAL_ROOTS %q: %w", root, err)
		}
		st, err := os.Stat(abs)
		if err != nil || !st.IsDir() {
			return nil, fmt.Errorf("LOCAL_ROOTS %q does not exist or is not a directory", abs)
		}
		localRoots = append(localRoots, abs)
	}
	cfg.Git.LocalRoots = localRoots

	cloneRootRaw := cfg.Git.CloneRoot
	if cloneRootRaw == "" {
		cloneRootRaw = filepath.Join(os.TempDir(), "code-archaeologist-clones")
	}
	cloneRoot, err := expandPath(cloneRootRaw)
	if err != nil {
		return nil, fmt.Errorf("invalid CLONE_ROOT %q: %w", cloneRootRaw, err)
	}
	if err := os.MkdirAll(cloneRoot, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create CLONE_ROOT %q: %w", cloneRoot, err)
	}
	cfg.Git.CloneRoot = cloneRoot

	if cfg.Git.Timeout <= 0 {
		cfg.Git.Timeout = 600 * time.Second
	}
	if cfg.Git.MaxCommits <= 0 {
		cfg.Git.MaxCommits = 10000
	}
	if cfg.Git.MaxCommits > 50000 {
		cfg.Git.MaxCommits = 50000
	}

	if cfg.Analysis.BatchSize <= 0 {
		cfg.Analysis.BatchSize = 20
	}
	if cfg.Analysis.BatchSize > 100 {
		cfg.Analysis.BatchSize = 100
	}
	if cfg.Analysis.MaxConcurrentJobs <= 0 {
		cfg.Analysis.MaxConcurrentJobs = 1
	}
	if cfg.Analysis.MaxConcurrentJobs > 10 {
		cfg.Analysis.MaxConcurrentJobs = 10
	}
	if cfg.Analysis.RequestTimeout <= 0 {
		cfg.Analysis.RequestTimeout = 1800 * time.Second
	}
	if cfg.Analysis.JobTimeout <= 0 {
		cfg.Analysis.JobTimeout = 7200 * time.Second
	}

	if cfg.Cascade.MaxParallel <= 0 {
		cfg.Cascade.MaxParallel = 1
	}
	if cfg.Cascade.MaxParallel > 10 {
		cfg.Cascade.MaxParallel = 10
	}
	if cfg.Cascade.ReduceSize <= 0 {
		cfg.Cascade.ReduceSize = 50
	}

	if cfg.Diff.MaxSizePerCommit <= 0 {
		cfg.Diff.MaxSizePerCommit = 3000
	}
	if cfg.Diff.MaxSizePerCommit > 10000 {
		cfg.Diff.MaxSizePerCommit = 10000
	}

	if cfg.RateLimitAnalyzePerMinute < 0 {
		cfg.RateLimitAnalyzePerMinute = 0
	}
	if cfg.RateLimitReadPerMinute < 0 {
		cfg.RateLimitReadPerMinute = 0
	}

	return cfg, nil
}

func loadDotEnv(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if key == "" {
			continue
		}

		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}

		if os.Getenv(key) == "" {
			os.Setenv(key, val)
		}
	}

	return true
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func getEnvInt(key string, def int) int {
	v := getEnv(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getEnvFloat(key string, def float64) float64 {
	v := getEnv(key, "")
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

func getEnvBool(key string, def bool) bool {
	v := getEnv(key, "")
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

func getEnvList(key string, def []string) []string {
	v := getEnv(key, "")
	if v == "" {
		return def
	}

	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func expandPath(p string) (string, error) {
	if p == "" {
		return "", nil
	}

	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if p == "~" {
			return home, nil
		}
		if strings.HasPrefix(p, "~/") {
			return filepath.Join(home, p[2:]), nil
		}
	}

	return filepath.Abs(p)
}
