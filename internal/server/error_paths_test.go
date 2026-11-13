package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Every database-backed endpoint: 503 with no database configured, and a
// generic 500 (no driver detail) when the database fails.
func TestDatabaseEndpoints_UnavailableAndFailing(t *testing.T) {
	endpoints := []struct{ method, path string }{
		{"GET", "/api/v1/stats"},
		{"GET", "/api/v1/stats/genres"},
		{"GET", "/api/v1/stats/dimensions"},
		{"GET", "/api/v1/stats/history"},
		{"GET", "/api/v1/history"},
		{"GET", "/api/v1/history/1"},
		{"DELETE", "/api/v1/history/1"},
		{"DELETE", "/api/v1/history/1?hard=true"},
		{"POST", "/api/v1/history/1/promote"},
	}
	dbless := apiServer(t, nil, nil)
	st := newSQLite(t)
	_ = st.Close() // every query on a closed database fails
	broken := apiServer(t, nil, st)

	for _, e := range endpoints {
		t.Run(e.method+" "+e.path, func(t *testing.T) {
			rec := send(t, dbless, e.method, e.path, nil)
			if rec.Code != http.StatusServiceUnavailable {
				t.Errorf("no database: code = %d, want 503 (%s)", rec.Code, rec.Body)
			}
			rec = send(t, broken, e.method, e.path, nil)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("failing database: code = %d, want 500 (%s)", rec.Code, rec.Body)
			}
			if body := rec.Body.String(); strings.Contains(body, "closed") ||
				strings.Contains(body, "sql") {
				t.Errorf("500 leaks driver detail: %s", body)
			}
		})
	}
}

func TestCORS(t *testing.T) {
	cfg := apiConfig()
	s := New(
		cfg,
		nil,
		minimalEngine(cfg),
		false,
		"",
		nil,
		"",
		[]string{"https://ui.example"},
		false,
	)
	get := func(method, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/v1/dimensions", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		s.router.ServeHTTP(rec, r)
		return rec
	}

	for _, origin := range []string{"https://ui.example", "HTTPS://UI.EXAMPLE"} {
		rec := get("GET", origin)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != origin {
			t.Errorf("origin %q: Allow-Origin = %q, want it echoed", origin, got)
		}
		if rec.Header().Get("Vary") != "Origin" || !strings.Contains(
			rec.Header().Get("Access-Control-Allow-Methods"), "PATCH") {
			t.Errorf("origin %q: missing Vary/Allow-Methods: %v", origin, rec.Header())
		}
	}
	for _, origin := range []string{"https://evil.example", ""} {
		if got := get("GET", origin).Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("origin %q: Allow-Origin = %q, want none", origin, got)
		}
	}
	// Preflight is answered by the middleware and never reaches a handler.
	if rec := get(
		"OPTIONS",
		"https://ui.example",
	); rec.Code != http.StatusNoContent ||
		rec.Body.Len() != 0 {
		t.Errorf("preflight: %d %q, want 204 and empty body", rec.Code, rec.Body)
	}
}

func TestSecurityHeaders(t *testing.T) {
	rec := send(t, apiServer(t, nil, nil), "GET", "/api/v1/dimensions", nil)
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" ||
		rec.Header().Get("X-Frame-Options") == "" {
		t.Errorf("security headers missing: %v", rec.Header())
	}
}

// The publish endpoint writes to AniList, so it is limited to rateLimitPublish
// requests per minute per client; the next one is refused.
func TestScorePublish_RateLimited(t *testing.T) {
	al, calls := fakeAniList(
		t,
		"tok",
		func(aniListCall) (int, string) { return 200, publishedEntry },
	)
	s := apiServer(t, al, nil)
	body := publishRequest{MediaID: 1, Score: 8}

	for i := range rateLimitPublish {
		if rec := send(t, s, "POST", "/api/v1/score/publish", body); rec.Code != 200 {
			t.Fatalf("request %d: code %d, want 200", i+1, rec.Code)
		}
	}
	rec := send(t, s, "POST", "/api/v1/score/publish", body)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("request %d: code %d, want 429", rateLimitPublish+1, rec.Code)
	}
	if len(*calls) != rateLimitPublish {
		t.Errorf("AniList got %d publishes, want %d (the refused one must not reach it)",
			len(*calls), rateLimitPublish)
	}
}
