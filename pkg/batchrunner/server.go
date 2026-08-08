package batchrunner

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/liip/sheriff"
)

type Server struct {
	store  *Store
	runner *Runner
}

func NewServer(store *Store, runner *Runner) *Server {
	return &Server{
		store:  store,
		runner: runner,
	}
}

func (s *Server) Handler() http.Handler {
	validate, err := newTokenValidator()
	if err != nil {
		log.Fatalf("Failed to set up the jwt validator: %v", err)
	}
	return requireAdmin(s.routes(), validate)
}

func (s *Server) handlerWithTokenValidator(validate tokenValidator) http.Handler {
	return requireAdmin(s.routes(), validate)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/plan", s.handlePlan)
	mux.HandleFunc("/runs", s.handleRuns)
	mux.HandleFunc("/runs/", s.handleRunPath)
	return mux
}

func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	plan := BuildPlan()
	if r.URL.Query().Get("view") == "web" {
		reduced, marshalErr := sheriff.Marshal(&sheriff.Options{Groups: []string{"web-plan"}}, plan)
		if marshalErr != nil {
			writeError(w, http.StatusInternalServerError, marshalErr.Error())
			return
		}
		writeJSON(w, http.StatusOK, reduced)
		return
	}
	if r.URL.Query().Get("view") != "" {
		writeError(w, http.StatusBadRequest, "unsupported view")
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) handleRuns(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		runs, err := s.store.ListRuns()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if r.URL.Query().Get("view") == "summary" {
			reduced, marshalErr := sheriff.Marshal(&sheriff.Options{Groups: []string{"web-run-summary"}}, runs)
			if marshalErr != nil {
				writeError(w, http.StatusInternalServerError, marshalErr.Error())
				return
			}
			writeJSON(w, http.StatusOK, reduced)
			return
		}
		if r.URL.Query().Get("view") != "" {
			writeError(w, http.StatusBadRequest, "unsupported view")
			return
		}
		writeJSON(w, http.StatusOK, runs)
	case http.MethodPost:
		view := r.URL.Query().Get("view")
		if view != "" && view != "summary" {
			writeError(w, http.StatusBadRequest, "unsupported view")
			return
		}

		var options RunOptions
		if err := json.NewDecoder(r.Body).Decode(&options); err != nil {
			writeError(w, http.StatusBadRequest, "invalid run options")
			return
		}

		run, err := s.runner.StartRun(options)
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		if view == "summary" {
			reduced, marshalErr := sheriff.Marshal(&sheriff.Options{Groups: []string{"web-run-summary"}}, run)
			if marshalErr != nil {
				writeError(w, http.StatusInternalServerError, marshalErr.Error())
				return
			}
			writeJSON(w, http.StatusCreated, reduced)
			return
		}
		writeJSON(w, http.StatusCreated, run)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleRunPath(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/runs/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	runID := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		run, err := s.store.LoadRun(runID)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, os.ErrNotExist) {
				status = http.StatusNotFound
			}
			writeError(w, status, err.Error())
			return
		}
		if r.URL.Query().Get("view") == "detail" {
			reduced, marshalErr := sheriff.Marshal(&sheriff.Options{Groups: []string{"web-run-detail"}}, run)
			if marshalErr != nil {
				writeError(w, http.StatusInternalServerError, marshalErr.Error())
				return
			}
			writeJSON(w, http.StatusOK, reduced)
			return
		}
		if r.URL.Query().Get("view") != "" {
			writeError(w, http.StatusBadRequest, "unsupported view")
			return
		}
		writeJSON(w, http.StatusOK, run)
		return
	}

	if len(parts) == 2 && parts[1] == "cancel" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if err := s.runner.CancelRun(runID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"cancelled": true})
		return
	}

	if len(parts) == 4 && parts[1] == "tasks" && parts[3] == "log" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.handleTaskLog(w, r, runID, parts[2])
		return
	}

	writeError(w, http.StatusNotFound, "not found")
}

func (s *Server) handleTaskLog(w http.ResponseWriter, r *http.Request, runID string, taskID string) {
	run, err := s.store.LoadRun(runID)
	if err != nil {
		writeError(w, http.StatusNotFound, "run not found")
		return
	}

	found := false
	for _, task := range run.Tasks {
		if task.ID == taskID {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}

	path := s.store.LogPath(runID, taskID)
	if !strings.HasPrefix(filepath.Clean(path), filepath.Clean(s.store.Root())) {
		writeError(w, http.StatusBadRequest, "invalid log path")
		return
	}

	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	defer file.Close()

	offsetValue := r.URL.Query().Get("offset")
	if offsetValue == "" {
		data, readErr := io.ReadAll(file)
		if readErr != nil {
			writeError(w, http.StatusInternalServerError, readErr.Error())
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(data)
		return
	}

	offset, parseErr := strconv.ParseInt(offsetValue, 10, 64)
	if parseErr != nil || offset < 0 {
		writeError(w, http.StatusBadRequest, "offset must be a non-negative integer")
		return
	}
	stat, statErr := file.Stat()
	if statErr != nil {
		writeError(w, http.StatusInternalServerError, statErr.Error())
		return
	}
	if offset > stat.Size() {
		offset = 0
	}
	if _, seekErr := file.Seek(offset, io.SeekStart); seekErr != nil {
		writeError(w, http.StatusInternalServerError, seekErr.Error())
		return
	}
	const maximumLogChunkBytes = 256 * 1024
	data, readErr := io.ReadAll(io.LimitReader(file, maximumLogChunkBytes))
	if readErr != nil {
		writeError(w, http.StatusInternalServerError, readErr.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Log-Next-Offset", strconv.FormatInt(offset+int64(len(data)), 10))
	_, _ = w.Write(data)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
