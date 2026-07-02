package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kondanta/kansou/internal/store/sqlite"
)

// TestExportCmd_EndToEnd wires the real cobra command, a real on-disk
// SQLite database, and the real export package together and exercises
// `kansou export` exactly as a user would.
func TestExportCmd_EndToEnd(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "kansou.db")
	st, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	seedOneScore(t, ctx, st)

	app := &App{Store: st}
	outputPath := filepath.Join(t.TempDir(), "export.html")

	exportCmd := app.exportCmd()
	exportCmd.SetContext(ctx)
	if err := exportCmd.Flags().Set("output", outputPath); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := exportCmd.RunE(exportCmd, nil); err != nil {
			t.Fatalf("export: %v", err)
		}
	})
	if !strings.Contains(out, "✓ Export written to") {
		t.Errorf("missing success message, got: %q", out)
	}

	html, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("reading exported file: %v", err)
	}
	if !strings.Contains(string(html), "Test Show") {
		t.Error("exported HTML missing seeded entry")
	}
}

// TestExportCmd_DefaultFilename confirms the default output path uses
// today's date when --output is omitted.
func TestExportCmd_DefaultFilename(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "kansou.db")
	st, err := sqlite.New(dbPath)
	if err != nil {
		t.Fatalf("opening store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	tmpDir := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	app := &App{Store: st}
	exportCmd := app.exportCmd()
	exportCmd.SetContext(ctx)
	if err := exportCmd.RunE(exportCmd, nil); err != nil {
		t.Fatalf("export: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(tmpDir, "kansou-export-*.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one default-named export file, got %v", matches)
	}
}

// TestExportCmd_DBless confirms the DBless error path fires before any
// store method is touched.
func TestExportCmd_DBless(t *testing.T) {
	app := &App{Store: nil}
	exportCmd := app.exportCmd()
	exportCmd.SetContext(context.Background())

	err := exportCmd.RunE(exportCmd, nil)
	if err == nil {
		t.Fatal("expected an error in DBless mode")
	}
	if !strings.Contains(err.Error(), "KANSOU_DB_TYPE") {
		t.Errorf("error message should mention KANSOU_DB_TYPE, got: %v", err)
	}
}
