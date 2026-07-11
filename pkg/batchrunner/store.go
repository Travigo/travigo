package batchrunner

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type Store struct {
	root string
	mu   sync.Mutex
}

func NewStore(root string) (*Store, error) {
	if root == "" {
		root = "/batch-runs"
	}

	store := &Store{root: root}
	for _, dir := range []string{store.runsDir(), store.logsDir()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	return store, nil
}

func (s *Store) SaveRun(run *Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if run == nil {
		return errors.New("run is nil")
	}

	if err := os.MkdirAll(s.runsDir(), 0o755); err != nil {
		return err
	}

	path := s.runPath(run.ID)
	tmp, err := os.CreateTemp(s.runsDir(), "."+sanitizePathPart(run.ID)+"-*.json")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	encoder := json.NewEncoder(tmp)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(run); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmpPath, path)
}

func (s *Store) LoadRun(id string) (*Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.runPath(id))
	if err != nil {
		return nil, err
	}

	var run Run
	if err := json.Unmarshal(data, &run); err != nil {
		return nil, err
	}

	return &run, nil
}

func (s *Store) ListRuns() ([]Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(s.runsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []Run{}, nil
		}
		return nil, err
	}

	runs := []Run{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		data, err := os.ReadFile(filepath.Join(s.runsDir(), entry.Name()))
		if err != nil {
			return nil, err
		}

		var run Run
		if err := json.Unmarshal(data, &run); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}

	sort.Slice(runs, func(i, j int) bool {
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})

	return runs, nil
}

func (s *Store) PrepareLog(runID string, taskID string) (string, error) {
	dir := filepath.Join(s.logsDir(), sanitizePathPart(runID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	path := filepath.Join(dir, sanitizePathPart(taskID)+".log")
	file, err := os.Create(path)
	if err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}

	return path, nil
}

func (s *Store) LogPath(runID string, taskID string) string {
	return filepath.Join(s.logsDir(), sanitizePathPart(runID), sanitizePathPart(taskID)+".log")
}

func (s *Store) Root() string {
	return s.root
}

func (s *Store) runsDir() string {
	return filepath.Join(s.root, "runs")
}

func (s *Store) logsDir() string {
	return filepath.Join(s.root, "logs")
}

func (s *Store) runPath(id string) string {
	return filepath.Join(s.runsDir(), sanitizePathPart(id)+".json")
}

func sanitizePathPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}

	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-', r == '_', r == '.':
			builder.WriteRune(r)
		default:
			builder.WriteRune('-')
		}
	}

	return builder.String()
}
