package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// dbCmd returns the `db` cobra command and its subcommands.
func (a *App) dbCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Database maintenance commands",
		Long:  "Commands for inspecting and maintaining the kansou score database.",
	}
	cmd.AddCommand(a.dbPruneCmd())
	return cmd
}

// dbPruneCmd returns the `db prune` cobra command.
func (a *App) dbPruneCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "prune",
		Short: "Hard-delete soft-deleted score records",
		Long: `Hard-delete all soft-deleted score records from the database.

Soft-deleted records are created by max_history enforcement: when a new score
is saved for an entry, older scores beyond max_history are soft-deleted.
Pruning permanently removes those records and any media entries with no
remaining scores. This operation is irreversible.

Requires a database (KANSOU_DB_TYPE must be set).`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return a.runDBPrune(cmd.Context())
		},
	}
}

// runDBPrune prompts for confirmation and then hard-deletes all soft-deleted records.
func (a *App) runDBPrune(ctx context.Context) error {
	if a.Store == nil {
		return fmt.Errorf("db prune requires a database — set KANSOU_DB_TYPE to enable")
	}

	fmt.Print("This will permanently delete all soft-deleted score entries. Continue? [y/N]: ")
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil || strings.ToLower(strings.TrimSpace(line)) != "y" {
		fmt.Println("Cancelled.")
		return nil
	}

	n, pruneErr := a.Store.Prune(ctx)
	if pruneErr != nil {
		return fmt.Errorf("pruning database: %w", pruneErr)
	}

	fmt.Printf("✓ Pruned %d score entries\n", n)
	return nil
}
