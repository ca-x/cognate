package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/ca-x/cognate/internal/install"
	"github.com/spf13/cobra"
)

func newBackupLocalCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "backup-local SLUG",
		Short: "实验性：备份已安装的本地 skill（仅 Linux）",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if err := command.Context().Err(); err != nil {
				return err
			}
			if runtime.GOOS != "linux" {
				return fmt.Errorf("local backup is unsupported on this platform")
			}
			if err := install.ValidateSlug(args[0]); err != nil {
				return err
			}
			home, err := localHome()
			if err != nil {
				return err
			}
			directory := filepath.Join(home, ".local", "share", "skillhub")
			if err := validateStateDirectory(directory, false); err != nil {
				return err
			}
			exists, err := validateDatabasePath(directory)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("no local installation database")
			}
			session, err := install.OpenSession(command.Context(), directory)
			if err != nil {
				return err
			}
			record, backupErr := session.Installation(command.Context(), args[0])
			if backupErr == nil {
				found := false
				for _, projection := range record.Projections {
					if projection.Kind != "canonical" {
						continue
					}
					if found {
						backupErr = fmt.Errorf("installation has multiple canonical directories")
						break
					}
					found = true
					if err := validateTrustedDirectory(projection.Path); err != nil {
						backupErr = fmt.Errorf("untrusted backup source: %w", err)
						break
					}
				}
				if !found && backupErr == nil {
					backupErr = fmt.Errorf("installation has no canonical directory")
				}
			}
			var backupPath, backupID string
			if backupErr == nil {
				backup, err := session.BackupLocal(command.Context(), args[0])
				backupPath, backupID, backupErr = backup.Path, backup.ID, err
			}
			if err := errors.Join(backupErr, session.Close()); err != nil {
				if backupID != "" {
					return fmt.Errorf("backup %s completed but cleanup failed: %w", terminalText(backupID), err)
				}
				return err
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "备份 %s\t%s\n", terminalText(backupID), terminalText(backupPath))
			if err != nil {
				return fmt.Errorf("backup %s completed but reporting failed: %w", terminalText(backupID), err)
			}
			return nil
		},
	}
}

func newBackupListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "backup-list",
		Short: "列出本地备份记录（仅 Linux）",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := command.Context().Err(); err != nil {
				return err
			}
			if runtime.GOOS != "linux" {
				return fmt.Errorf("local backup is unsupported on this platform")
			}
			home, err := localHome()
			if err != nil {
				return err
			}
			directory := filepath.Join(home, ".local", "share", "skillhub")
			if _, err := os.Lstat(directory); errors.Is(err, os.ErrNotExist) {
				_, err = fmt.Fprintln(command.OutOrStdout(), "没有本地备份记录")
				return err
			} else if err != nil {
				return err
			}
			if err := validateStateDirectory(directory, false); err != nil {
				return err
			}
			exists, err := validateDatabasePath(directory)
			if err != nil {
				return err
			}
			if !exists {
				_, err = fmt.Fprintln(command.OutOrStdout(), "没有本地备份记录")
				return err
			}
			session, err := install.OpenSession(command.Context(), directory)
			if err != nil {
				return err
			}
			records, listErr := session.Backups(command.Context())
			if err := errors.Join(listErr, session.Close()); err != nil {
				return err
			}
			if len(records) == 0 {
				_, err = fmt.Fprintln(command.OutOrStdout(), "没有本地备份记录")
				return err
			}
			for _, record := range records {
				if err := command.Context().Err(); err != nil {
					return err
				}
				if _, err := fmt.Fprintf(command.OutOrStdout(), "%s\t%s\t%s\t%s\n", terminalText(record.ID), terminalText(record.Slug), terminalText(record.Format), terminalText(record.Path)); err != nil {
					return err
				}
			}
			return command.Context().Err()
		},
	}
}
