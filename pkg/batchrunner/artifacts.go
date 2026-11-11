package batchrunner

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// StageArtifact atomically publishes an artifact produced by prepare. The returned
// opaque token is a capability URL segment; an empty token means the artifact did
// not change and no file was published.
func (s *Store) StageArtifact(runID string, prepare func(*os.File) (changed bool, etag string, err error)) (string, error) {
	if !validArtifactRunID(runID) {
		return "", errors.New("invalid run ID")
	}
	if prepare == nil {
		return "", errors.New("artifact prepare function is nil")
	}
	var tokenBytes [24]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(tokenBytes[:])
	baseDir := filepath.Join(s.root, "artifacts", runID)
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return "", err
	}
	tmpDir, err := os.MkdirTemp(baseDir, ".stage-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	file, err := os.OpenFile(filepath.Join(tmpDir, "content"), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	if err != nil {
		return "", err
	}
	changed, etag, prepareErr := prepare(file)
	if prepareErr == nil && changed {
		prepareErr = file.Sync()
	}
	if closeErr := file.Close(); prepareErr == nil && closeErr != nil {
		prepareErr = closeErr
	}
	if prepareErr != nil {
		return "", prepareErr
	}
	if !changed {
		return "", nil
	}
	// Store the upstream validator alongside the complete content before publishing
	// the directory, so readers can never observe a partially staged artifact.
	if err := os.WriteFile(filepath.Join(tmpDir, "etag"), []byte(etag), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmpDir, filepath.Join(baseDir, token)); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Store) RemoveArtifact(runID, token string) error {
	if !validArtifactRunID(runID) || !validArtifactToken(token) {
		return errors.New("invalid artifact path")
	}
	err := os.RemoveAll(filepath.Join(s.root, "artifacts", runID, token))
	if err != nil {
		return err
	}
	return nil
}

func (s *Store) artifactExists(runID, token string) (bool, error) {
	if !validArtifactRunID(runID) || !validArtifactToken(token) {
		return false, errors.New("invalid artifact path")
	}
	dir := filepath.Join(s.root, "artifacts", runID, token)
	for _, name := range []string{"content", "etag"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("invalid staged artifact %s", name)
		}
	}
	return true, nil
}

func (s *Store) RemoveRunArtifacts(runID string) error {
	if !validArtifactRunID(runID) {
		return errors.New("invalid run ID")
	}
	return s.removeRunArtifacts(runID)
}

func (s *Store) removeRunArtifacts(runID string) error {
	return os.RemoveAll(filepath.Join(s.root, "artifacts", runID))
}

// ArtifactHandler serves artifacts only when both the run ID and opaque token
// are supplied. It intentionally has no listing or mutation endpoint.
func (s *Store) ArtifactHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "private, no-store")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		const prefix = "/artifacts/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, prefix), "/")
		if len(parts) != 2 || !validArtifactRunID(parts[0]) || !validArtifactToken(parts[1]) {
			http.NotFound(w, r)
			return
		}
		dir := filepath.Join(s.root, "artifacts", parts[0], parts[1])
		contentPath := filepath.Join(dir, "content")
		etagData, err := os.ReadFile(filepath.Join(dir, "etag"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		etag := string(etagData)
		if etag != "" {
			w.Header().Set("ETag", etag)
			if r.Header.Get("If-None-Match") == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		file, err := os.Open(contentPath)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = io.Copy(w, file)
		}
	})
}

func validArtifactRunID(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func validArtifactToken(value string) bool {
	if len(value) != 48 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
