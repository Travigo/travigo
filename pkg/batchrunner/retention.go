package batchrunner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	runLogRetention   = 14 * 24 * time.Hour
	orphanArtifactAge = 24 * time.Hour
)

// Cleanup removes expired logs and artifacts while retaining run history.
// Nonterminal runs always keep their logs and artifacts, regardless of age.
func (s *Store) Cleanup(now time.Time) error {
	runs, err := s.ListRuns()
	if err != nil {
		return fmt.Errorf("list batch runs for cleanup: %w", err)
	}

	runByID := make(map[string]Run, len(runs))
	for _, run := range runs {
		if validArtifactRunID(run.ID) {
			runByID[run.ID] = run
		}
	}

	var cleanupErrors []error
	if err := s.removeStaleRunTemps(now.Add(-orphanArtifactAge)); err != nil {
		cleanupErrors = append(cleanupErrors, err)
	}
	logCutoff := now.Add(-runLogRetention)
	logEntries, err := os.ReadDir(s.logsDir())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("read batch log directories: %w", err))
	} else if err == nil {
		for _, entry := range logEntries {
			if !entry.IsDir() || !validArtifactRunID(entry.Name()) {
				continue
			}
			run, exists := runByID[entry.Name()]
			if exists {
				if !isTerminalRunStatus(run.Status) || run.FinishedAt == nil || !run.FinishedAt.Before(logCutoff) {
					continue
				}
			} else {
				info, infoErr := entry.Info()
				if infoErr != nil {
					cleanupErrors = append(cleanupErrors, fmt.Errorf("inspect orphan log directory %q: %w", entry.Name(), infoErr))
					continue
				}
				if !info.ModTime().Before(logCutoff) {
					continue
				}
			}
			if err := os.RemoveAll(filepath.Join(s.logsDir(), entry.Name())); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("remove log directory %q: %w", entry.Name(), err))
			}
		}
	}

	artifactEntries, err := os.ReadDir(filepath.Join(s.root, "artifacts"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("read batch artifact directories: %w", err))
	} else if err == nil {
		artifactCutoff := now.Add(-orphanArtifactAge)
		for _, entry := range artifactEntries {
			if !entry.IsDir() || !validArtifactRunID(entry.Name()) {
				continue
			}
			run, exists := runByID[entry.Name()]
			if exists {
				if !isTerminalRunStatus(run.Status) {
					continue
				}
			} else {
				info, infoErr := entry.Info()
				if infoErr != nil {
					cleanupErrors = append(cleanupErrors, fmt.Errorf("inspect orphan artifact directory %q: %w", entry.Name(), infoErr))
					continue
				}
				if !info.ModTime().Before(artifactCutoff) {
					continue
				}
			}
			if err := s.removeRunArtifacts(entry.Name()); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("remove artifact directory %q: %w", entry.Name(), err))
			}
		}
	}

	return errors.Join(cleanupErrors...)
}

func (s *Store) removeStaleRunTemps(cutoff time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.runsDir())
	if err != nil {
		return err
	}
	var cleanupErrors []error
	for _, entry := range entries {
		if entry.IsDir() || !isRunTempFile(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err == nil && info.ModTime().Before(cutoff) {
			err = os.Remove(filepath.Join(s.runsDir(), entry.Name()))
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}

func isTerminalRunStatus(status RunStatus) bool {
	switch status {
	case RunStatusSucceeded, RunStatusFailed, RunStatusCancelled:
		return true
	default:
		return false
	}
}
