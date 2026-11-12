package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kondanta/kansou/internal/store"
	"github.com/kondanta/kansou/internal/store/sqlite"
)

func TestProbes(t *testing.T) {
	cfg := minimalConfig()

	healthy, err := sqlite.New(filepath.Join(t.TempDir(), "ok.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	t.Cleanup(func() { _ = healthy.Close() })

	broken, err := sqlite.New(filepath.Join(t.TempDir(), "broken.db"))
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	_ = broken.Close() // closed DB: every Ping fails

	mk := func(st store.Store, dbType string) *Server {
		return New(cfg, nil, minimalEngine(cfg), false, "", st, dbType, nil, false)
	}

	tests := []struct {
		name       string
		srv        *Server
		path       string
		wantCode   int
		wantStatus string
		wantChecks map[string]string
	}{
		{"health stateless", mk(nil, ""), "/health", http.StatusOK, "", nil},
		{"ready stateless", mk(nil, ""), "/ready", http.StatusOK, "ready", map[string]string{}},
		{
			"ready sqlite ok", mk(healthy, "sqlite"), "/ready",
			http.StatusOK, "ready",
			map[string]string{"database": "ok"},
		},
		{
			"ready sqlite down", mk(broken, "sqlite"), "/ready",
			http.StatusServiceUnavailable, "not ready",
			map[string]string{"database": "fail"},
		},
		{"health ignores broken db", mk(broken, "sqlite"), "/health", http.StatusOK, "", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.srv.router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if rec.Code != tc.wantCode {
				t.Fatalf("code = %d, want %d", rec.Code, tc.wantCode)
			}
			if tc.wantStatus == "" {
				return
			}
			var got readyResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", got.Status, tc.wantStatus)
			}
			for k, v := range tc.wantChecks {
				if got.Checks[k] != v {
					t.Errorf("checks[%s] = %q, want %q", k, got.Checks[k], v)
				}
			}
			if strings.Contains(rec.Body.String(), "closed") {
				t.Errorf("body leaks DB error detail: %s", rec.Body.String())
			}
		})
	}
}
