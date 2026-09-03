package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"code-archaeologist/backend/internal/llm"
	"code-archaeologist/backend/internal/scanner"
)

// fakeCache — in-memory реализация DecisionCache для тестов.
type fakeCache struct {
	mu    sync.Mutex
	data  map[string]map[string][]byte
	saved [][]string
}

func newFakeCache() *fakeCache { return &fakeCache{data: make(map[string]map[string][]byte)} }

func (c *fakeCache) Load(sourceKey string, hashes []string) (map[string][]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string][]byte)
	for _, h := range hashes {
		if raw, ok := c.data[sourceKey][h]; ok {
			out[h] = raw
		}
	}
	return out, nil
}

func (c *fakeCache) Save(sourceKey string, hashes []string, decisionsJSON []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.data[sourceKey] == nil {
		c.data[sourceKey] = make(map[string][]byte)
	}
	for _, h := range hashes {
		c.data[sourceKey][h] = decisionsJSON
	}
	c.saved = append(c.saved, hashes)
	return nil
}

// fakeProvider считает вызовы и возвращает фиксированный JSON.
type fakeProvider struct {
	calls int
}

func (f *fakeProvider) Name() string         { return "fake" }
func (f *fakeProvider) DefaultModel() string { return "fake-model" }
func (f *fakeProvider) Chat(ctx context.Context, messages []llm.Message, opts llm.ChatOptions) (llm.Response, error) {
	f.calls++
	return llm.Response{
		Content: `[{"title":"T","decision":"D","tags":["x"]}]`,
		Usage:   llm.Usage{TotalTokens: 1},
	}, nil
}

func mkCommits(n int) []scanner.CommitWithDiff {
	commits := make([]scanner.CommitWithDiff, 0, n)
	for i := 0; i < n; i++ {
		commits = append(commits, scanner.CommitWithDiff{
			Commit: scanner.Commit{
				Hash:    fmt.Sprintf("h%02d000000000000000000000000000000000000", i),
				Subject: fmt.Sprintf("commit %d", i),
			},
		})
	}
	return commits
}

func TestIncrementalReusesCache(t *testing.T) {
	cache := newFakeCache()
	prov := &fakeProvider{}

	p := Params{SourceKey: "test-source", Incremental: true, Cache: cache}

	// Первый прогон: 5 коммитов, все анализируются и сохраняются.
	if _, err := Run(context.Background(), prov, p, mkCommits(5), 5); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if prov.calls != 1 {
		t.Fatalf("expected 1 LLM call, got %d", prov.calls)
	}

	// Второй прогон: те же коммиты — ни одного вызова LLM.
	if _, err := Run(context.Background(), prov, p, mkCommits(5), 5); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if prov.calls != 1 {
		t.Errorf("expected cache hit (still 1 call), got %d", prov.calls)
	}

	// Третий прогон: +2 новых коммита — один вызов только для нового батча.
	commits := mkCommits(7)
	if _, err := Run(context.Background(), prov, p, commits, 5); err != nil {
		t.Fatalf("third run: %v", err)
	}
	if prov.calls != 2 {
		t.Errorf("expected 1 more call for fresh batch, got %d total", prov.calls)
	}
}

func TestIncrementalDisabled(t *testing.T) {
	cache := newFakeCache()
	prov := &fakeProvider{}

	p := Params{SourceKey: "test-source", Incremental: false, Cache: cache}

	if _, err := Run(context.Background(), prov, p, mkCommits(5), 5); err != nil {
		t.Fatalf("run: %v", err)
	}
	if prov.calls != 1 {
		t.Fatalf("expected 1 call, got %d", prov.calls)
	}
	if len(cache.saved) != 0 {
		t.Error("cache must not be used when incremental is disabled")
	}
}

func TestIncrementalCascade(t *testing.T) {
	cache := newFakeCache()
	prov := &fakeProvider{}

	p := Params{SourceKey: "src", Incremental: true, Cache: cache}
	cfg := CascadeConfig{Enabled: true, MaxParallel: 2, ReduceSize: 10, BatchSize: 3}

	if _, err := RunCascade(context.Background(), prov, p, mkCommits(6), cfg, nil); err != nil {
		t.Fatalf("cascade run: %v", err)
	}

	before := prov.calls
	if _, err := RunCascade(context.Background(), prov, p, mkCommits(6), cfg, nil); err != nil {
		t.Fatalf("cascade run 2: %v", err)
	}
	// Ожидаем только 1 вызов — generateOverview; map и reduce полностью из кэша.
	if prov.calls != before+1 {
		t.Errorf("expected only overview call, calls changed %d -> %d", before, prov.calls)
	}
}

func TestCacheContentRoundtrip(t *testing.T) {
	cache := newFakeCache()
	prov := &fakeProvider{}
	p := Params{SourceKey: "src", Incremental: true, Cache: cache}

	commits := mkCommits(3)
	if _, err := Run(context.Background(), prov, p, commits, 3); err != nil {
		t.Fatalf("run: %v", err)
	}

	hash := commits[0].Hash
	raw, err := cache.Load("src", []string{hash})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	var decisions []Decision
	if err := json.Unmarshal(raw[hash], &decisions); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(decisions) == 0 || decisions[0].Title != "T" {
		t.Errorf("unexpected cached decisions: %+v", decisions)
	}
}
