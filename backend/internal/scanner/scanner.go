package scanner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"code-archaeologist/backend/internal/config"
)

type Commit struct {
	Hash        string
	AuthorName  string
	AuthorEmail string
	Date        string
	Subject     string
	Body        string
}

type CommitWithDiff struct {
	Commit
	Diff string
}

// CommitFilter ограничивает выборку git log. Пустые поля означают «без ограничения».
type CommitFilter struct {
	Since      string // YYYY-MM-DD
	Until      string // YYYY-MM-DD
	FromCommit string // ref-исключение диапазона (хэш, тег, ветка)
	ToCommit   string // ref-вершина диапазона (хэш, тег, ветка)
}

var (
	safeRefRe  = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._/-]{0,127}$`)
	safeDateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// ValidateCommitFilter отклоняет значения, которые нельзя безопасно передать
// в argv для git (инъекция опций, диапазонные операторы, мусорные даты).
func ValidateCommitFilter(f CommitFilter) error {
	for _, r := range []struct{ name, val string }{
		{"from_commit", f.FromCommit},
		{"to_commit", f.ToCommit},
	} {
		if r.val == "" {
			continue
		}
		if strings.Contains(r.val, "..") {
			return fmt.Errorf("%s must not contain '..'", r.name)
		}
		if !safeRefRe.MatchString(r.val) {
			return fmt.Errorf("invalid %s", r.name)
		}
	}

	for _, d := range []struct{ name, val string }{
		{"since", f.Since},
		{"until", f.Until},
	} {
		if d.val == "" {
			continue
		}
		if !safeDateRe.MatchString(d.val) {
			return fmt.Errorf("%s must be in YYYY-MM-DD format", d.name)
		}
	}

	return nil
}

type PreparedRepo struct {
	Path    string
	Cleanup func()
}

type Service struct {
	cfg config.GitConfig
}

type DiffProgressFunc func(current, total int, hash string)

func NewService(cfg config.GitConfig) *Service {
	return &Service{cfg: cfg}
}

func (s *Service) Prepare(ctx context.Context, sourceType, source string) (*PreparedRepo, error) {
	sourceType = strings.ToLower(strings.TrimSpace(sourceType))
	source = strings.TrimSpace(source)

	if source == "" {
		return nil, errors.New("source is empty")
	}
	if strings.HasPrefix(source, "-") {
		return nil, errors.New("source must not start with '-'")
	}

	switch sourceType {
	case "local":
		path, err := s.validateLocal(source)
		if err != nil {
			return nil, err
		}
		return &PreparedRepo{
			Path:    path,
			Cleanup: func() {},
		}, nil

	case "github", "gitlab":
		remoteURL, err := s.validateRemoteURL(source)
		if err != nil {
			return nil, err
		}

		base, err := os.MkdirTemp(s.cfg.CloneRoot, "repo-")
		if err != nil {
			return nil, fmt.Errorf("failed to create temporary clone directory: %w", err)
		}

		dest := filepath.Join(base, "repository.git")

		_, err = runGit(
			ctx,
			s.cfg.Timeout,
			"-c", "http.followRedirects=false",
			"clone", "--quiet", "--bare",
			remoteURL,
			dest,
		)
		if err != nil {
			os.RemoveAll(base)
			return nil, fmt.Errorf("git clone failed: %w", err)
		}

		return &PreparedRepo{
			Path: dest,
			Cleanup: func() {
				os.RemoveAll(base)
			},
		}, nil

	default:
		return nil, fmt.Errorf("unsupported source type %q", sourceType)
	}
}

func (s *Service) Commits(ctx context.Context, repoPath string, limit int, filter CommitFilter) ([]Commit, error) {
	repoPath = strings.TrimSpace(repoPath)
	if repoPath == "" {
		return nil, errors.New("repository path is empty")
	}
	if strings.HasPrefix(repoPath, "-") {
		return nil, errors.New("repository path must not start with '-'")
	}

	if err := ValidateCommitFilter(filter); err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = 200
	}
	if limit > s.cfg.MaxCommits {
		limit = s.cfg.MaxCommits
	}

	args := []string{
		"-C", repoPath,
		"--no-pager",
		"log",
		"--no-color",
	}

	// Семантика диапазона: from..to, from..HEAD либо «всё до to».
	switch {
	case filter.FromCommit != "" && filter.ToCommit != "":
		args = append(args, filter.FromCommit+".."+filter.ToCommit)
	case filter.FromCommit != "":
		args = append(args, filter.FromCommit+"..HEAD")
	case filter.ToCommit != "":
		args = append(args, filter.ToCommit)
	}

	if filter.Since != "" {
		args = append(args, "--since="+filter.Since)
	}
	if filter.Until != "" {
		args = append(args, "--until="+filter.Until)
	}

	args = append(args,
		fmt.Sprintf("--max-count=%d", limit),
		"--pretty=format:%H%x1f%an%x1f%ae%x1f%aI%x1f%s%x1f%b%x1e",
	)

	out, err := runGit(ctx, s.cfg.Timeout, args...)
	if err != nil {
		return nil, fmt.Errorf("git log failed: %w", err)
	}

	commits, err := parseCommits(out)
	if err != nil {
		return nil, err
	}

	if len(commits) == 0 {
		return nil, errors.New("repository has no commits")
	}

	return commits, nil
}

func (s *Service) CommitsWithDiff(ctx context.Context, repoPath string, commits []Commit, maxDiff int, onProgress DiffProgressFunc) ([]CommitWithDiff, error) {
	result := make([]CommitWithDiff, 0, len(commits))
	total := len(commits)
	for i, c := range commits {
		if onProgress != nil {
			onProgress(i+1, total, c.Hash)
		}
		diff, err := s.getCommitDiff(ctx, repoPath, c.Hash, maxDiff)
		if err != nil {
			diff = fmt.Sprintf("[ошибка получения diff: %v]", err)
		}
		result = append(result, CommitWithDiff{
			Commit: c,
			Diff:   diff,
		})
	}
	return result, nil
}

func (s *Service) getCommitDiff(ctx context.Context, repoPath, hash string, maxSize int) (string, error) {
	if maxSize <= 0 {
		maxSize = 3000
	}

	args := []string{
		"-C", repoPath,
		"--no-pager",
		"show",
		"--no-color",
		"--stat",
		hash,
	}

	out, err := runGit(ctx, 30*time.Second, args...)
	if err != nil {
		return "", err
	}

	if len(out) > maxSize {
		out = out[:maxSize] + "\n... [обрезано]"
	}

	return out, nil
}

func (s *Service) validateLocal(source string) (string, error) {
	source = expandHome(source)
	if strings.HasPrefix(source, "-") {
		return "", errors.New("local path must not start with '-'")
	}

	abs, err := filepath.Abs(source)
	if err != nil {
		return "", fmt.Errorf("invalid local path: %w", err)
	}

	resolved := abs
	if sym, err := filepath.EvalSymlinks(abs); err == nil {
		resolved = sym
	}

	st, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("local path does not exist: %w", err)
	}
	if !st.IsDir() {
		return "", errors.New("local path is not a directory")
	}

	if len(s.cfg.LocalRoots) > 0 {
		ok := false
		for _, root := range s.cfg.LocalRoots {
			if isWithin(resolved, root) {
				ok = true
				break
			}
		}
		if !ok {
			return "", errors.New("local path is outside allowed LOCAL_ROOTS")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := runGit(ctx, 0, "-C", resolved, "rev-parse", "--git-dir"); err != nil {
		return "", errors.New("local path is not a git repository")
	}

	return resolved, nil
}

func (s *Service) validateRemoteURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("remote URL is empty")
	}
	if len(raw) > 2048 {
		return "", errors.New("remote URL is too long")
	}
	if strings.HasPrefix(raw, "-") {
		return "", errors.New("remote URL must not start with '-'")
	}
	if strings.ContainsAny(raw, "\x00\n\r\t") {
		return "", errors.New("remote URL contains invalid characters")
	}

	if strings.HasPrefix(raw, "git@") {
		rest := raw[4:]
		idx := strings.Index(rest, ":")
		if idx <= 0 || idx == len(rest)-1 {
			return "", errors.New("invalid scp-like git URL")
		}
		host := strings.ToLower(rest[:idx])
		if !s.hostAllowed(host) {
			return "", fmt.Errorf("host %q is not allowed", host)
		}
		return raw, nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid remote URL: %w", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "ssh" {
		return "", errors.New("remote URL scheme must be http, https or ssh")
	}

	host := strings.ToLower(u.Hostname())
	if !s.hostAllowed(host) {
		return "", fmt.Errorf("host %q is not allowed", host)
	}

	return raw, nil
}

func (s *Service) hostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}

	for _, allowed := range s.cfg.AllowedHosts {
		if host == allowed {
			return true
		}
	}
	return false
}

func runGit(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	setGitEnv(cmd)

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()
	out := buf.String()

	if err != nil {
		return out, fmt.Errorf("%w: %s", err, truncateText(out, 1024))
	}

	return out, nil
}

func setGitEnv(cmd *exec.Cmd) {
	cmd.Env = append(
		os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
	)
}

func parseCommits(out string) ([]Commit, error) {
	records := strings.Split(out, "\x1e")
	commits := make([]Commit, 0, len(records))

	for _, rec := range records {
		rec = strings.TrimLeft(rec, "\n\r ")
		if rec == "" {
			continue
		}

		fields := strings.Split(rec, "\x1f")
		if len(fields) < 6 {
			continue
		}

		body := strings.Join(fields[5:], "\x1f")

		c := Commit{
			Hash:        strings.TrimSpace(fields[0]),
			AuthorName:  strings.TrimSpace(fields[1]),
			AuthorEmail: strings.TrimSpace(fields[2]),
			Date:        strings.TrimSpace(fields[3]),
			Subject:     strings.TrimSpace(fields[4]),
			Body:        strings.TrimSpace(body),
		}

		if c.Hash == "" {
			continue
		}

		commits = append(commits, c)
	}

	return commits, nil
}

func isWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func expandHome(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}

	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

func truncateText(s string, limit int) string {
	if limit <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "…"
}
