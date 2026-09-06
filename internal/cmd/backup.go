package cmd

import (
	"fmt"
	"os"

	"github.com/ahmedelgabri/ccpeek/internal/db"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(&cobra.Command{
		Use: "backup <destination>", Short: "Write a verified SQLite archive snapshot, including WAL data", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			legacy, err := resolveDataFile(cmd)
			if err != nil {
				return err
			}
			path, err := resolveIndexFile(cmd, legacy)
			if err != nil {
				return err
			}
			if _, err := os.Stat(path); err != nil {
				return err
			}
			if err := db.BackupFile(cmd.Context(), path, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Backup written to %s\n", args[0])
			return nil
		},
	})
	rootCmd.AddCommand(&cobra.Command{
		Use: "restore <backup> --index-file <new-archive>", Short: "Verify and restore a backup into a new archive path", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("index-file") {
				return fmt.Errorf("restore requires --index-file naming a new, nonexistent archive")
			}
			path, err := resolveIndexFile(cmd, "")
			if err != nil {
				return err
			}
			if err := db.Restore(cmd.Context(), args[0], path); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Archive restored to %s\n", path)
			return nil
		},
	})
}
