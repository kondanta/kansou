package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kondanta/kansou/internal/store/sqlite"
)

// withStdin redirects os.Stdin to input for the duration of fn, feeding it
// through a pipe so interactive prompts (confirmations, pickers) in the code
// under test read exactly what the test supplies.
func withStdin(t *testing.T, input string, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}
	old := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = old }()

	go func() {
		defer func() { _ = w.Close() }()
		_, _ = io.WriteString(w, input)
	}()

	fn()
}

// TestHistoryCmd_EndToEnd wires the real cobra command, a real on-disk
// SQLite database, and the real Store implementation together — no fakes —
// and exercises kansou history / show / delete exactly as a user would.
func TestHistoryCmd_EndToEnd(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "kansou.db")
	st, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	seedOneScore(t, ctx, st)

	app := &App{Store: st}
	historyCmdTree := app.historyCmd()
	historyCmdTree.SetContext(ctx)

	t.Run("bare list", func(t *testing.T) {
		out := captureStdout(t, func() {
			if err := historyCmdTree.RunE(historyCmdTree, nil); err != nil {
				t.Fatalf("history: %v", err)
			}
		})
		if !strings.Contains(out, "Test Show") {
			t.Errorf("list output missing seeded entry, got: %q", out)
		}
	})

	t.Run("show by numeric AniList ID", func(t *testing.T) {
		showCmd := app.historyShowCmd()
		showCmd.SetContext(ctx)
		out := captureStdout(t, func() {
			if err := showCmd.RunE(showCmd, []string{"1"}); err != nil {
				t.Fatalf("history show: %v", err)
			}
		})
		if !strings.Contains(out, "Test Show") || !strings.Contains(out, "Story") {
			t.Errorf("show output missing expected content, got: %q", out)
		}
	})

	t.Run("show unknown ID errors", func(t *testing.T) {
		showCmd := app.historyShowCmd()
		showCmd.SetContext(ctx)
		if err := showCmd.RunE(showCmd, []string{"999999"}); err == nil {
			t.Error("expected an error for a nonexistent AniList ID")
		}
	})

	t.Run("delete with confirmation", func(t *testing.T) {
		deleteCmd := app.historyDeleteCmd()
		deleteCmd.SetContext(ctx)
		var out string
		withStdin(t, "y\n", func() {
			out = captureStdout(t, func() {
				if err := deleteCmd.RunE(deleteCmd, []string{"1"}); err != nil {
					t.Fatalf("history delete: %v", err)
				}
			})
		})
		if !strings.Contains(out, "marked for deletion") {
			t.Errorf("delete output missing confirmation, got: %q", out)
		}

		// Deliberate delete does not promote an older score — the entry must
		// disappear from the bare list entirely (no older score existed here).
		listOut := captureStdout(t, func() {
			if err := historyCmdTree.RunE(historyCmdTree, nil); err != nil {
				t.Fatalf("history: %v", err)
			}
		})
		if strings.Contains(listOut, "Test Show") {
			t.Errorf("deleted entry should no longer appear in the list, got: %q", listOut)
		}
	})

	t.Run("delete declined leaves the entry intact", func(t *testing.T) {
		seedOneScore(t, ctx, st) // re-seed after the previous subtest deleted it

		deleteCmd := app.historyDeleteCmd()
		deleteCmd.SetContext(ctx)
		withStdin(t, "n\n", func() {
			captureStdout(t, func() {
				if err := deleteCmd.RunE(deleteCmd, []string{"1"}); err != nil {
					t.Fatalf("history delete: %v", err)
				}
			})
		})

		listOut := captureStdout(t, func() {
			if err := historyCmdTree.RunE(historyCmdTree, nil); err != nil {
				t.Fatalf("history: %v", err)
			}
		})
		if !strings.Contains(listOut, "Test Show") {
			t.Errorf("declined delete should leave the entry intact, got: %q", listOut)
		}
	})
}

// TestHistoryCmd_DBless confirms the DBless error path fires for every
// history subcommand before any store method is touched.
func TestHistoryCmd_DBless(t *testing.T) {
	app := &App{Store: nil}
	ctx := context.Background()

	for _, cmd := range []struct {
		name string
		run  func() error
	}{
		{"history", func() error {
			c := app.historyCmd()
			c.SetContext(ctx)
			return c.RunE(c, nil)
		}},
		{"history show", func() error {
			c := app.historyShowCmd()
			c.SetContext(ctx)
			return c.RunE(c, []string{"1"})
		}},
		{"history delete", func() error {
			c := app.historyDeleteCmd()
			c.SetContext(ctx)
			return c.RunE(c, []string{"1"})
		}},
	} {
		t.Run(cmd.name, func(t *testing.T) {
			err := cmd.run()
			if err == nil {
				t.Fatal("expected an error in DBless mode")
			}
			if !strings.Contains(err.Error(), "KANSOU_DB_TYPE") {
				t.Errorf("error message should mention KANSOU_DB_TYPE, got: %v", err)
			}
		})
	}
}
