package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/ca-x/cognate/internal/install"
	"github.com/spf13/cobra"
)

func newRestoreLocalCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "restore-local BACKUP_ID PARENT NAME",
		Short: "实验性：将本地备份恢复为新目录（仅 Linux）",
		Args:  cobra.ExactArgs(3),
		RunE: func(command *cobra.Command, args []string) error {
			if err := command.Context().Err(); err != nil {
				return err
			}
			if runtime.GOOS != "linux" {
				return fmt.Errorf("local restore is unsupported on this platform")
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
				return fmt.Errorf("no local backup database")
			}
			parent, err := resolveTrustedDirectory(args[1])
			if err != nil {
				return fmt.Errorf("untrusted restore parent: %w", err)
			}
			if _, err := fmt.Fprintln(command.ErrOrStderr(), "恢复目录使用备份的原始权限，可能允许其他用户读取或写入。"); err != nil {
				return err
			}
			session, err := install.OpenSession(command.Context(), directory)
			if err != nil {
				return err
			}
			restoreErr := session.RestoreLocal(command.Context(), args[0], parent, args[2])
			if err := errors.Join(restoreErr, session.Close()); err != nil {
				return err
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "已恢复 %s\n", terminalText(filepath.Join(parent, args[2])))
			if err != nil {
				return fmt.Errorf("restore completed but reporting failed: %w", err)
			}
			return nil
		},
	}
}
