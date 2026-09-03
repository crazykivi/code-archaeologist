package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type MemoryStore struct {
	mu        sync.RWMutex
	jobs      map[string]*Job
	reports   map[string]*Report
	providers map[string]ProviderConfig
	decisions map[string]map[string][]byte
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		jobs:      make(map[string]*Job),
		reports:   make(map[string]*Report),
		providers: make(map[string]ProviderConfig),
		decisions: make(map[string]map[string][]byte),
	}
}

func (s *MemoryStore) Close() error { return nil }

func memoryNewID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *MemoryStore) CreateJob(req Request) (*Job, error) {
	id, err := memoryNewID()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	j := &Job{
		ID:        id,
		Status:    StatusQueued,
		CreatedAt: now,
		Request:   req,
	}

	s.mu.Lock()
	s.jobs[id] = j
	s.mu.Unlock()

	jCopy := *j
	return &jCopy, nil
}

func (s *MemoryStore) GetJob(id string) (*Job, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	j, ok := s.jobs[id]
	if !ok {
		return nil, false
	}
	jCopy := *j
	return &jCopy, true
}

func (s *MemoryStore) ListJobs(limit int) ([]*Job, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	all := make([]*Job, 0, len(s.jobs))
	for _, j := range s.jobs {
		jCopy := *j
		all = append(all, &jCopy)
	}

	sort.Slice(all, func(i, k int) bool {
		return all[i].CreatedAt.After(all[k].CreatedAt)
	})

	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

func (s *MemoryStore) MarkRunning(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	j, ok := s.jobs[id]
	if !ok {
		return false
	}
	j.Status = StatusRunning
	j.StartedAt = time.Now().UTC()
	return true
}

func (s *MemoryStore) MarkFailed(id, publicErr string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	j, ok := s.jobs[id]
	if !ok {
		return false
	}
	j.Status = StatusFailed
	j.Error = publicErr
	j.FinishedAt = time.Now().UTC()
	return true
}

func (s *MemoryStore) MarkCompleted(id, reportID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	j, ok := s.jobs[id]
	if !ok {
		return false
	}
	j.Status = StatusCompleted
	j.ReportID = reportID
	j.FinishedAt = time.Now().UTC()
	return true
}

func (s *MemoryStore) UpdateProgress(id string, progress *Progress) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	j, ok := s.jobs[id]
	if !ok {
		return false
	}
	j.Progress = progress
	return true
}

func (s *MemoryStore) SaveReport(markdown string) (*Report, error) {
	id, err := memoryNewID()
	if err != nil {
		return nil, err
	}

	r := &Report{
		ID:        id,
		CreatedAt: time.Now().UTC(),
		Markdown:  markdown,
	}

	s.mu.Lock()
	s.reports[id] = r
	s.mu.Unlock()

	rCopy := *r
	return &rCopy, nil
}

func (s *MemoryStore) GetReport(id string) (*Report, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	r, ok := s.reports[id]
	if !ok {
		return nil, false
	}
	rCopy := *r
	return &rCopy, true
}

func (s *MemoryStore) DeleteJob(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.jobs[id]; !ok {
		return false
	}
	delete(s.jobs, id)
	return true
}

func (s *MemoryStore) ListProviderConfigs() ([]ProviderConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	all := make([]ProviderConfig, 0, len(s.providers))
	for _, pc := range s.providers {
		all = append(all, pc)
	}
	sort.Slice(all, func(i, k int) bool { return all[i].Name < all[k].Name })
	return all, nil
}

func (s *MemoryStore) GetProviderConfig(name string) (*ProviderConfig, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	pc, ok := s.providers[strings.TrimSpace(name)]
	if !ok {
		return nil, false
	}
	copy := pc
	return &copy, true
}

func (s *MemoryStore) SaveProviderConfig(pc ProviderConfig) error {
	pc.Name = strings.TrimSpace(pc.Name)
	if pc.Name == "" {
		return fmt.Errorf("provider name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	pc.UpdatedAt = time.Now().UTC()
	s.providers[pc.Name] = pc
	return nil
}

func (s *MemoryStore) DeleteProviderConfig(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.providers, strings.TrimSpace(name))
	return nil
}

func (s *MemoryStore) UpdateUsage(id string, prompt, completion, total int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	j, ok := s.jobs[id]
	if !ok {
		return false
	}
	j.PromptTokens = prompt
	j.CompletionTokens = completion
	j.TotalTokens = total
	return true
}

func (s *MemoryStore) SaveCommitDecisions(sourceKey string, hashes []string, decisionsJSON []byte) error {
	if strings.TrimSpace(sourceKey) == "" || len(hashes) == 0 || len(decisionsJSON) == 0 {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	bySource, ok := s.decisions[sourceKey]
	if !ok {
		bySource = make(map[string][]byte)
		s.decisions[sourceKey] = bySource
	}
	for _, h := range hashes {
		if h != "" {
			cp := make([]byte, len(decisionsJSON))
			copy(cp, decisionsJSON)
			bySource[h] = cp
		}
	}
	return nil
}

func (s *MemoryStore) LoadCommitDecisions(sourceKey string, hashes []string) (map[string][]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make(map[string][]byte, len(hashes))
	bySource, ok := s.decisions[sourceKey]
	if !ok {
		return out, nil
	}
	for _, h := range hashes {
		if data, ok := bySource[h]; ok {
			cp := make([]byte, len(data))
			copy(cp, data)
			out[h] = cp
		}
	}
	return out, nil
}
