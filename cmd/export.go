package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/kondanta/kansou/internal/export"
)

// exportCmd returns the `export` cobra command.
func (a *App) exportCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Export scoring history and stats to a self-contained HTML file",
		Long: `Generate a single HTML file with charts and tables summarising your
scoring history. The file is self-contained — no server or network access
is needed to view it.

Requires a database (KANSOU_DB_TYPE must be set).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if a.Store == nil {
				return fmt.Errorf("export requires a database — set KANSOU_DB_TYPE to enable")
			}
			return runExport(cmd, a, output)
		},
	}
	cmd.Flags().StringVar(&output, "output", "", "Output file path (default: kansou-export-YYYY-MM-DD.html)")
	return cmd
}

// runExport generates the HTML export and writes it to output, defaulting
// the filename to today's date when output is empty.
func runExport(cmd *cobra.Command, a *App, output string) error {
	if output == "" {
		output = fmt.Sprintf("kansou-export-%s.html", time.Now().Format("2006-01-02"))
	}

	html, err := export.Generate(cmd.Context(), a.Store)
	if err != nil {
		return fmt.Errorf("generating export: %w", err)
	}
	if err := os.WriteFile(output, html, 0o644); err != nil {
		return fmt.Errorf("writing export file: %w", err)
	}
	fmt.Printf("✓ Export written to %s\n", output)
	return nil
}
