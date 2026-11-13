package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kondanta/kansou/internal/store"
)

// pingStore is a store whose only working method is Ping. The embedded nil
// interface makes any other call panic, which is what we want in a probe test.
type pingStore struct {
	store.Store
	ping func(ctx context.Context) error
}

func (p pingStore) Ping(ctx context.Context) error { return p.ping(ctx) }

func readyServer(t *testing.T, ping func(ctx context.Context) error) *Server {
	t.Helper()
	cfg := minimalConfig()
	return New(
		cfg,
		nil,
		minimalEngine(cfg),
		false,
		"",
		pingStore{ping: ping},
		"postgres",
		nil,
		false,
	)
}

func getReady(srv *Server, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	return rec
}

// A database that never answers must fail the probe after exactly 2s, not hang
// the handler. The literal is deliberate: the Helm readiness probe's
// timeoutSeconds (charts/kansou/templates/deployment.yaml) must exceed it, so
// changing readyTimeout has to be a conscious edit of both. synctest's fake
// clock makes the wait instant and the deadline exact.
func TestReady_HungDatabaseFailsAfterReadyTimeout(t *testing.T) {
	srv := readyServer(t, func(ctx context.Context) error {
		<-ctx.Done() // a hung database: only the deadline ends the call
		return ctx.Err()
	})
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		rec := getReady(srv, httptest.NewRequest(http.MethodGet, "/ready", nil))
		if elapsed := time.Since(start); elapsed != 2*time.Second {
			t.Errorf("handler took %v, want exactly 2s", elapsed)
		}
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("code = %d, want 503", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `"database":"fail"`) {
			t.Errorf("body = %s, want database fail", rec.Body.String())
		}
	})
}

// Ping must receive a context carrying the readyTimeout deadline, derived from
// the request context.
func TestReady_PingGetsDeadline(t *testing.T) {
	var deadline time.Time
	var hasDeadline bool
	srv := readyServer(t, func(ctx context.Context) error {
		deadline, hasDeadline = ctx.Deadline()
		return nil
	})
	synctest.Test(t, func(t *testing.T) {
		now := time.Now()
		rec := getReady(srv, httptest.NewRequest(http.MethodGet, "/ready", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("code = %d, want 200", rec.Code)
		}
		if !hasDeadline || deadline.Sub(now) != readyTimeout {
			t.Errorf(
				"deadline = %v (set: %v), want now+%v",
				deadline.Sub(now),
				hasDeadline,
				readyTimeout,
			)
		}
	})
}

// A client that disconnects mid-probe cancels the request context, which must
// reach Ping.
func TestReady_ClientCancelReachesPing(t *testing.T) {
	srv := readyServer(t, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		req := httptest.NewRequest(http.MethodGet, "/ready", nil).WithContext(ctx)
		time.AfterFunc(100*time.Millisecond, cancel)

		start := time.Now()
		rec := getReady(srv, req)
		if elapsed := time.Since(start); elapsed != 100*time.Millisecond {
			t.Errorf("handler returned after %v, want 100ms (the cancel)", elapsed)
		}
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("code = %d, want 503", rec.Code)
		}
	})
}

// The probe tracks the database live: it must not cache a failure.
func TestReady_RecoversWhenDatabaseReturns(t *testing.T) {
	var down atomic.Bool
	down.Store(true)
	srv := readyServer(t, func(context.Context) error {
		if down.Load() {
			return errors.New("connection refused")
		}
		return nil
	})
	req := func() int { return getReady(srv, httptest.NewRequest(http.MethodGet, "/ready", nil)).Code }

	if got := req(); got != http.StatusServiceUnavailable {
		t.Fatalf("while down: code = %d, want 503", got)
	}
	down.Store(false)
	if got := req(); got != http.StatusOK {
		t.Fatalf("after recovery: code = %d, want 200", got)
	}
	down.Store(true)
	if got := req(); got != http.StatusServiceUnavailable {
		t.Fatalf("after second outage: code = %d, want 503", got)
	}
}

// Failure detail (which may contain hostnames or credentials) is logged, never
// returned to the caller.
func TestReady_ErrorDetailNotReturned(t *testing.T) {
	const secret = "dial tcp 10.0.0.5:5432: password=hunter2"
	srv := readyServer(t, func(context.Context) error { return errors.New(secret) })
	rec := getReady(srv, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", rec.Code)
	}
	for _, leak := range []string{"hunter2", "10.0.0.5", "dial tcp"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Errorf("response leaks %q: %s", leak, rec.Body.String())
		}
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}
}

// Liveness must not touch the database at all, even a hung one.
func TestHealth_NeverPingsDatabase(t *testing.T) {
	var pings atomic.Int32
	srv := readyServer(t, func(context.Context) error { pings.Add(1); return errors.New("down") })
	rec := getReady(srv, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK || pings.Load() != 0 {
		t.Errorf("code = %d, pings = %d; want 200 and 0 pings", rec.Code, pings.Load())
	}
}
