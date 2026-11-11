package manager

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestStagedFetchRetriesUnavailableServerAndInterruptedBody(t *testing.T) {
	const payload = "the complete feed"
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch attempts.Add(1) {
		case 1:
			w.WriteHeader(http.StatusServiceUnavailable)
		case 2:
			w.Header().Set("Content-Length", "100")
			_, _ = io.WriteString(w, "partial")
		default:
			w.Header().Set("ETag", `"publisher-tag"`)
			_, _ = io.WriteString(w, payload)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "staged")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	etag, err := fetchStagedSource(server.Client(), req, file, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if etag != `"publisher-tag"` || string(body) != payload || attempts.Load() != 3 {
		t.Fatalf("got ETag %q, body %q and %d requests", etag, body, attempts.Load())
	}
}

func TestNationalRailLoginReturnsErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		token  string
	}{
		{http.StatusOK, `{"token":"publisher-token"}`, "publisher-token"},
		{http.StatusUnauthorized, `{"token":"publisher-token"}`, ""},
		{http.StatusOK, `{}`, ""},
	} {
		t.Run(fmt.Sprintf("%d %s", tc.status, tc.body), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || r.FormValue("username") != "test-user" || r.FormValue("password") != "test-password" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			token, err := customAuthNationalRailLogin(t.Context(), server.Client(), server.URL, "test-user", "test-password")
			if token != tc.token || (err != nil) != (tc.token == "") {
				t.Fatalf("got token %q, error %v", token, err)
			}
		})
	}
}
