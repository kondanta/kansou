package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kondanta/kansou/internal/store"
)

// TestHistoryEndpoints_EndToEnd wires a real chi router, a real on-disk
// SQLite database, and the real Store implementation together and drives
// every /history* endpoint over actual HTTP.
func TestHistoryEndpoints_EndToEnd(t *testing.T) {
	s, st := newDBBackedTestServer(t)
	seedTwoScores(t, context.Background(), st)

	var scoreIDToDelete int

	t.Run("list", func(t *testing.T) {
		var body []historyListItem
		res := doGet(t, s, "/api/history", &body)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200", res.StatusCode)
		}
		if len(body) != 2 {
			t.Fatalf("got %d entries, want 2: %+v", len(body), body)
		}
		titles := map[string]int{}
		for _, item := range body {
			titles[item.TitleRomaji] = item.ScoreID
		}
		if _, ok := titles["Test Show A"]; !ok {
			t.Errorf("expected Test Show A in list, got %+v", body)
		}
		scoreIDToDelete = titles["Test Show A"]
	})

	t.Run("detail", func(t *testing.T) {
		var body []store.Score
		res := doGet(t, s, "/api/history/1", &body)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("status: got %d, want 200", res.StatusCode)
		}
		if len(body) != 1 {
			t.Fatalf("got %d scores, want 1: %+v", len(body), body)
		}
		if len(body[0].Breakdown) != 2 {
			t.Errorf("expected full breakdown (2 dimensions), got %+v", body[0].Breakdown)
		}
	})

	t.Run("detail invalid id", func(t *testing.T) {
		var body errorResponse
		res := doGet(t, s, "/api/history/not-a-number", &body)
		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("status: got %d, want 400", res.StatusCode)
		}
	})

	t.Run("delete", func(t *testing.T) {
		req := doDelete(t, s, fmt.Sprintf("/api/history/%d", scoreIDToDelete))
		if req.StatusCode != http.StatusNoContent {
			t.Fatalf("status: got %d, want 204", req.StatusCode)
		}

		// Deliberate delete does not promote an older score — must disappear
		// from the list, not just drop a count.
		var body []historyListItem
		doGet(t, s, "/api/history", &body)
		for _, item := range body {
			if item.TitleRomaji == "Test Show A" {
				t.Errorf("deleted entry still present in list: %+v", item)
			}
		}
	})

	t.Run("delete already deleted", func(t *testing.T) {
		req := doDelete(t, s, fmt.Sprintf("/api/history/%d", scoreIDToDelete))
		if req.StatusCode != http.StatusNotFound {
			t.Fatalf("status: got %d, want 404", req.StatusCode)
		}
	})

	t.Run("delete invalid id", func(t *testing.T) {
		req := doDelete(t, s, "/api/history/not-a-number")
		if req.StatusCode != http.StatusBadRequest {
			t.Fatalf("status: got %d, want 400", req.StatusCode)
		}
	})
}

// TestHistoryEndpoints_DBless confirms every /history* endpoint returns the
// documented 503 envelope when no store is configured.
func TestHistoryEndpoints_DBless(t *testing.T) {
	cfg := minimalConfig()
	s := New(cfg, nil, minimalEngine(cfg), true, "", nil, "", nil)

	t.Run("GET /history", func(t *testing.T) {
		var body errorResponse
		res := doGet(t, s, "/api/history", &body)
		if res.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status: got %d, want 503", res.StatusCode)
		}
	})
	t.Run("GET /history/{anilist_id}", func(t *testing.T) {
		var body errorResponse
		res := doGet(t, s, "/api/history/1", &body)
		if res.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status: got %d, want 503", res.StatusCode)
		}
	})
	t.Run("DELETE /history/{score_id}", func(t *testing.T) {
		res := doDelete(t, s, "/api/history/1")
		if res.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("status: got %d, want 503", res.StatusCode)
		}
	})
}

// doDelete issues a DELETE request against the server's real router and
// returns the raw response (for status-code-only assertions).
func doDelete(t *testing.T, s *Server, path string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, path, nil)
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	return rec.Result()
}
