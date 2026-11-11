package batchrunner

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupRetainsHistoryAndNonterminalFiles(t *testing.T) {
	root := t.TempDir()
	store, err := NewStore(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	interruptedWrite := filepath.Join(store.runsDir(), ".active-run-123.json")
	if err := os.WriteFile(interruptedWrite, []byte(`{"incomplete":`), 0o600); err != nil {
		t.Fatal(err)
	}
	setModTime(t, interruptedWrite, now.Add(-25*time.Hour))
	oldFinished := now.Add(-15 * 24 * time.Hour)
	recentFinished := now.Add(-10 * 24 * time.Hour)
	for _, run := range []Run{
		{ID: "active-run", Status: RunStatusRunning},
		{ID: "old-run", Status: RunStatusSucceeded, FinishedAt: &oldFinished},
		{ID: "recent-run", Status: RunStatusFailed, FinishedAt: &recentFinished},
	} {
		if err := store.SaveRun(&run); err != nil {
			t.Fatal(err)
		}
	}

	for _, id := range []string{"active-run", "old-run", "recent-run", "orphan-old", "orphan-new"} {
		if err := os.MkdirAll(filepath.Join(root, "logs", id), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "logs", id, "task.log"), []byte("log"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"active-run", "old-run", "recent-run", "orphan-old", "orphan-new"} {
		if err := os.MkdirAll(filepath.Join(root, "artifacts", id, "token"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "artifacts", id, "token", "content"), []byte("artifact"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setModTime(t, filepath.Join(root, "logs", "active-run"), now.Add(-30*24*time.Hour))
	setModTime(t, filepath.Join(root, "artifacts", "active-run"), now.Add(-30*24*time.Hour))
	setModTime(t, filepath.Join(root, "logs", "old-run"), now.Add(-20*24*time.Hour))
	setModTime(t, filepath.Join(root, "logs", "recent-run"), now.Add(-20*24*time.Hour))
	setModTime(t, filepath.Join(root, "logs", "orphan-old"), now.Add(-20*24*time.Hour))
	setModTime(t, filepath.Join(root, "logs", "orphan-new"), now.Add(-2*24*time.Hour))
	setModTime(t, filepath.Join(root, "artifacts", "orphan-old"), now.Add(-25*time.Hour))
	setModTime(t, filepath.Join(root, "artifacts", "orphan-new"), now.Add(-12*time.Hour))

	if err := store.Cleanup(now); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(interruptedWrite); !os.IsNotExist(err) {
		t.Fatalf("expected interrupted state write to be removed, got %v", err)
	}

	for _, path := range []string{
		"logs/active-run", "artifacts/active-run",
		"logs/recent-run", "logs/orphan-new", "artifacts/orphan-new",
	} {
		if _, err := os.Stat(filepath.Join(root, path)); err != nil {
			t.Errorf("expected %s to be retained: %v", path, err)
		}
	}
	for _, path := range []string{
		"logs/old-run", "logs/orphan-old",
		"artifacts/old-run", "artifacts/recent-run", "artifacts/orphan-old",
	} {
		if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed, stat error = %v", path, err)
		}
	}
	for _, id := range []string{"active-run", "old-run", "recent-run"} {
		if _, err := store.LoadRun(id); err != nil {
			t.Errorf("run history %q was removed: %v", id, err)
		}
	}
}

func setModTime(t *testing.T, path string, value time.Time) {
	t.Helper()
	if err := os.Chtimes(path, value, value); err != nil {
		t.Fatal(err)
	}
}
