package anilist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kondanta/kansou/internal/logger"
)

const testToken = "tok-SECRET-123"

// redirect sends every request to the test server instead of the real
// AniList endpoint, so the production code runs unmodified.
type redirect struct{ target *url.URL }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host = r.target.Scheme, r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

// gqlRequest is the decoded request body the server received.
type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

// newClient returns a Client talking to handler, plus the captured requests.
func newClient(t *testing.T, token string, handler http.HandlerFunc) (*Client, *[]gqlRequest) {
	t.Helper()
	var reqs []gqlRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var gr gqlRequest
		_ = json.NewDecoder(r.Body).Decode(&gr)
		reqs = append(reqs, gr)
		handler(w, r.WithContext(context.WithValue(r.Context(), reqKey{}, gr)))
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return &Client{
		http:  &http.Client{Transport: redirect{u}, Timeout: 5 * time.Second},
		token: token,
	}, &reqs
}

type reqKey struct{}

func reqOf(r *http.Request) gqlRequest { return r.Context().Value(reqKey{}).(gqlRequest) }

func respond(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(body))
}

// captureLogs returns a context whose logger records everything (debug and up).
func captureLogs() (context.Context, *bytes.Buffer) {
	var buf bytes.Buffer
	l := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return logger.WithContext(context.Background(), l), &buf
}

const mediaJSON = `{"id":154587,"title":{"romaji":"Sousou no Frieren","english":"Frieren","native":"x"},
"format":"TV","status":"FINISHED","episodes":28,"chapters":0,"genres":["Adventure","Fantasy"],
"tags":[{"name":"Elf","rank":90,"isMediaSpoiler":false}],"coverImage":{"extraLarge":"https://img/c.png"},
"bannerImage":"","averageScore":91,"meanScore":89}`

func TestNewClient_ReadsTokenFromEnv(t *testing.T) {
	t.Setenv(tokenEnvVar, "from-env")
	if c := NewClient(); c.token != "from-env" || c.http.Timeout != 15*time.Second {
		t.Errorf("token=%q timeout=%v, want from-env and 15s", c.token, c.http.Timeout)
	}
	t.Setenv(tokenEnvVar, "")
	if c := NewClient(); c.token != "" {
		t.Errorf("token = %q, want empty when unset", c.token)
	}
}

func TestSearchByNameMulti_Success(t *testing.T) {
	c, reqs := newClient(t, testToken, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("method=%s content-type=%q", r.Method, r.Header.Get("Content-Type"))
		}
		if a := r.Header.Get("Authorization"); a != "" {
			t.Errorf("read-only search must not send the token, got Authorization %q", a)
		}
		respond(w, `{"data":{"Page":{"media":[`+mediaJSON+`]}}}`)
	})

	got, err := c.SearchByNameMulti(context.Background(), "frieren", "ANIME")
	if err != nil || len(got) != 1 {
		t.Fatalf("got (%+v, %v)", got, err)
	}
	if m := got[0]; m.ID != 154587 || m.TitleEnglish != "Frieren" || m.MediaType != "ANIME" ||
		strings.Join(m.Genres, ",") != "adventure,fantasy" || m.CoverImage != "https://img/c.png" {
		t.Errorf("mapped media wrong: %+v", m)
	}
	v := (*reqs)[0].Variables
	if v["search"] != "frieren" || v["perPage"] != float64(searchPageSize) || v["type"] != "ANIME" {
		t.Errorf("variables = %v", v)
	}
}

func TestSearchByNameMulti_TypeOmittedWhenEmpty(t *testing.T) {
	c, reqs := newClient(t, "", func(w http.ResponseWriter, _ *http.Request) {
		respond(w, `{"data":{"Page":{"media":[`+mediaJSON+`]}}}`)
	})
	if _, err := c.SearchByNameMulti(context.Background(), "x", ""); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, has := (*reqs)[0].Variables["type"]; has {
		t.Errorf("type variable sent for empty mediaType: %v", (*reqs)[0].Variables)
	}
}

func TestSearchByNameMulti_NoResults(t *testing.T) {
	c, _ := newClient(t, "", func(w http.ResponseWriter, _ *http.Request) {
		respond(w, `{"data":{"Page":{"media":[]}}}`)
	})
	_, err := c.SearchByNameMulti(context.Background(), "zzzz", "")
	if err == nil || !strings.Contains(err.Error(), `no results found for "zzzz"`) {
		t.Errorf("got %v", err)
	}
}

func TestFetchByID(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{"found", `{"data":{"Media":` + mediaJSON + `}}`, ""},
		{"null media", `{"data":{"Media":null}}`, "no media found for AniList ID 154587"},
		{
			"graphql error",
			`{"errors":[{"message":"Not Found.","status":404}],"data":{"Media":null}}`,
			"AniList error: Not Found.",
		},
		{"malformed json", `{"data":`, "decoding AniList response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, reqs := newClient(
				t,
				"",
				func(w http.ResponseWriter, _ *http.Request) { respond(w, tt.body) },
			)
			got, err := c.FetchByID(context.Background(), 154587)
			if v := (*reqs)[0].Variables["id"]; v != float64(154587) {
				t.Errorf("id variable = %v", v)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("got (%v, %v), want error containing %q", got, err, tt.wantErr)
				}
				return
			}
			if err != nil || got.ID != 154587 {
				t.Errorf("got (%+v, %v)", got, err)
			}
		})
	}
}

func TestFetch_Non200IsUpstreamError(t *testing.T) {
	c, _ := newClient(t, testToken, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	})
	_, err := c.FetchByID(context.Background(), 1)
	up, ok := errors.AsType[*UpstreamError](err)
	if !ok || up.StatusCode != 429 {
		t.Fatalf("got %v, want *UpstreamError with status 429", err)
	}
	if !strings.Contains(err.Error(), "HTTP 429") ||
		!strings.Contains(err.Error(), "rate limited") {
		t.Errorf("message = %q", err.Error())
	}
	if errors.Unwrap(up) == nil {
		t.Error("UpstreamError must unwrap to its cause")
	}
}

func TestFetch_NetworkFailureIsUpstreamError(t *testing.T) {
	c, _ := newClient(t, testToken, func(http.ResponseWriter, *http.Request) {})
	// Point at a closed port: nothing is listening there.
	srv := httptest.NewServer(http.NotFoundHandler())
	u, _ := url.Parse(srv.URL)
	srv.Close()
	c.http.Transport = redirect{u}

	_, err := c.FetchByID(context.Background(), 1)
	up, ok := errors.AsType[*UpstreamError](err)
	if !ok || up.StatusCode != 0 || !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("got %v, want *UpstreamError status 0 mentioning unreachable", err)
	}
}

func TestFetch_CanceledContext(t *testing.T) {
	c, _ := newClient(t, "", func(w http.ResponseWriter, _ *http.Request) { respond(w, `{}`) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.FetchByID(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

func TestFetch_ErrorBodyIsCapped(t *testing.T) {
	c, _ := newClient(t, "", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(strings.Repeat("x", 3*maxErrorBodyBytes)))
	})
	_, err := c.FetchByID(context.Background(), 1)
	if err == nil || len(err.Error()) > maxErrorBodyBytes+100 {
		t.Errorf("error length %d, want at most ~%d", len(err.Error()), maxErrorBodyBytes)
	}
}

const entryJSON = `{"data":{"SaveMediaListEntry":{"id":777,"score":8.5,"status":"COMPLETED",
"media":{"title":{"romaji":"Sousou no Frieren"}}}}}`

func TestPublishScore_RequiresToken(t *testing.T) {
	var hits atomic.Int32
	c, _ := newClient(
		t,
		"",
		func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); respond(w, entryJSON) },
	)
	_, err := c.PublishScore(context.Background(), 1, 8.5, "")
	if err == nil || !strings.Contains(err.Error(), "ANILIST_TOKEN") {
		t.Fatalf("got %v, want a missing-token error", err)
	}
	if hits.Load() != 0 {
		t.Error("a request was sent even though no token is set")
	}
}

func TestPublishScore_WithoutNotes(t *testing.T) {
	c, reqs := newClient(t, testToken, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("Authorization = %q", got)
		}
		respond(w, entryJSON)
	})
	got, err := c.PublishScore(context.Background(), 154587, 8.5, "")
	if err != nil {
		t.Fatalf("PublishScore: %v", err)
	}
	if *got != (PublishResult{EntryID: 777, Score: 8.5, Status: "COMPLETED", TitleRomaji: "Sousou no Frieren"}) {
		t.Errorf("result = %+v", *got)
	}
	if len(*reqs) != 1 {
		t.Fatalf("%d requests, want exactly 1 (no notes lookup)", len(*reqs))
	}
	r := (*reqs)[0]
	if r.Variables["mediaId"] != float64(154587) || r.Variables["score"] != 8.5 {
		t.Errorf("variables = %v", r.Variables)
	}
	if _, has := r.Variables["notes"]; has || strings.Contains(r.Query, "$notes") {
		t.Error("notes must not be sent when empty")
	}
}

func TestPublishScore_Notes(t *testing.T) {
	tests := []struct {
		name      string
		notesResp string // response to the existing-notes lookup
		notesCode int
		want      string
	}{
		{"no list entry", `{"data":{"Media":{"mediaListEntry":null}}}`, 200, "new block"},
		{"empty notes", `{"data":{"Media":{"mediaListEntry":{"notes":""}}}}`, 200, "new block"},
		{
			"existing notes kept", `{"data":{"Media":{"mediaListEntry":{"notes":"old"}}}}`, 200,
			"old\n\n---\n\nnew block",
		},
		{"lookup fails: still publishes", `boom`, 500, "new block"},
		{"lookup returns garbage", `not json`, 200, "new block"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, reqs := newClient(t, testToken, func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(reqOf(r).Query, "mediaListEntry") {
					w.WriteHeader(tt.notesCode)
					_, _ = w.Write([]byte(tt.notesResp))
					return
				}
				respond(w, entryJSON)
			})
			if _, err := c.PublishScore(context.Background(), 1, 9, "new block"); err != nil {
				t.Fatalf("PublishScore: %v", err)
			}
			last := (*reqs)[len(*reqs)-1]
			if last.Variables["notes"] != tt.want || !strings.Contains(last.Query, "$notes") {
				t.Errorf("notes = %q (query has $notes: %v), want %q",
					last.Variables["notes"], strings.Contains(last.Query, "$notes"), tt.want)
			}
		})
	}
}

func TestPublishScore_Failures(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr string
	}{
		{"graphql error", func(w http.ResponseWriter, _ *http.Request) {
			respond(w, `{"errors":[{"message":"Invalid token","status":401}]}`)
		}, "AniList error: Invalid token"},
		{"no entry returned", func(w http.ResponseWriter, _ *http.Request) {
			respond(w, `{"data":{"SaveMediaListEntry":null}}`)
		}, "no entry after publish"},
		{"http 401", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		}, "HTTP 401"},
		{"malformed json", func(w http.ResponseWriter, _ *http.Request) {
			respond(w, `{`)
		}, "decoding AniList response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newClient(t, testToken, tt.handler)
			_, err := c.PublishScore(context.Background(), 1, 8, "")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

// The token must never reach an error message or a log line, on success or
// failure (CLAUDE.md: "Never log it. Never include it in error messages.").
func TestPublishScore_TokenNeverLeaks(t *testing.T) {
	statuses := []int{http.StatusOK, http.StatusUnauthorized, http.StatusInternalServerError}
	for _, status := range statuses {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			c, _ := newClient(t, testToken, func(w http.ResponseWriter, _ *http.Request) {
				if status != http.StatusOK {
					http.Error(w, "nope", status)
					return
				}
				respond(w, entryJSON)
			})
			ctx, logs := captureLogs()
			_, err := c.PublishScore(ctx, 1, 8, "some notes")
			if err != nil && strings.Contains(err.Error(), testToken) {
				t.Errorf("token in error: %v", err)
			}
			if strings.Contains(logs.String(), testToken) {
				t.Errorf("token in logs:\n%s", logs)
			}
			if logs.Len() == 0 {
				t.Error("no log output captured: the leak check proved nothing")
			}
		})
	}
}

func TestUpstreamError_Methods(t *testing.T) {
	cause := errors.New("boom")
	e := &UpstreamError{StatusCode: 502, err: cause}
	if e.Error() != "boom" || !errors.Is(e, cause) {
		t.Errorf("Error()=%q Is(cause)=%v", e.Error(), errors.Is(e, cause))
	}
}

func TestSearchByNameMulti_Failures(t *testing.T) {
	tests := []struct {
		name    string
		handler http.HandlerFunc
		wantErr string
	}{
		{"graphql error", func(w http.ResponseWriter, _ *http.Request) {
			respond(w, `{"errors":[{"message":"Too many requests","status":429}]}`)
		}, "AniList error: Too many requests"},
		{
			"malformed json", func(w http.ResponseWriter, _ *http.Request) { respond(w, `[`) },
			"decoding AniList response",
		},
		{"http 500", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "down", http.StatusInternalServerError)
		}, "HTTP 500: down"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newClient(t, "", tt.handler)
			_, err := c.SearchByNameMulti(context.Background(), "x", "")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("got %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

// A non-200 whose body cannot be read in full still yields an UpstreamError
// carrying the status code.
func TestFetch_UnreadableErrorBody(t *testing.T) {
	c, _ := newClient(t, "", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100") // promise more than we send
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("short"))
	})
	_, err := c.FetchByID(context.Background(), 1)
	up, ok := errors.AsType[*UpstreamError](err)
	if !ok || up.StatusCode != http.StatusBadGateway ||
		!strings.Contains(err.Error(), "could not read error body") {
		t.Fatalf("got %v, want *UpstreamError 502 'could not read error body'", err)
	}
}

func TestPublishScore_DebugLogRecordsWhetherNotesWereSent(t *testing.T) {
	for _, notes := range []string{"", "n"} {
		c, _ := newClient(t, testToken, func(w http.ResponseWriter, r *http.Request) {
			if strings.Contains(reqOf(r).Query, "mediaListEntry") {
				respond(w, `{"data":{"Media":{"mediaListEntry":null}}}`)
				return
			}
			respond(w, entryJSON)
		})
		ctx, logs := captureLogs()
		if _, err := c.PublishScore(ctx, 1, 8, notes); err != nil {
			t.Fatalf("PublishScore: %v", err)
		}
		if want := fmt.Sprintf(
			"with_notes=%t",
			notes != "",
		); !strings.Contains(
			logs.String(),
			want,
		) {
			t.Errorf("notes=%q: log lacks %q:\n%s", notes, want, logs)
		}
	}
}
