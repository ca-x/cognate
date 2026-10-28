package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/ca-x/skillhub/internal/install"
	"github.com/ca-x/skillhub/internal/state"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
)

func newInstallLocalCommand() *cobra.Command {
	var slug string
	command := &cobra.Command{
		Use:   "install-local DIRECTORY",
		Short: "实验性：从本地目录安装 skill（仅 Linux）",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			if err := command.Context().Err(); err != nil {
				return err
			}
			if runtime.GOOS != "linux" {
				return fmt.Errorf("local installation is unsupported on this platform")
			}
			if err := install.ValidateSlug(slug); err != nil {
				return err
			}
			home, err := localHome()
			if err != nil {
				return err
			}
			canonical := filepath.Join(home, ".agents", "skills")
			directory := filepath.Join(home, ".local", "share", "skillhub")
			if err := prepareStateDirectory(directory); err != nil {
				return err
			}
			if _, err := validateDatabasePath(directory); err != nil {
				return err
			}
			if _, err := os.Lstat(canonical); errors.Is(err, os.ErrNotExist) {
				if err := createTrustedDirectory(canonical, false); err != nil {
					return fmt.Errorf("prepare canonical directory: %w", err)
				}
			} else if err != nil {
				return fmt.Errorf("inspect canonical directory: %w", err)
			}
			session, err := install.OpenSession(command.Context(), directory)
			if err != nil {
				return err
			}
			source, installErr := validateLocalDirectories(home, args[0])
			alreadyPresent := false
			if installErr == nil {
				alreadyPresent, installErr = session.InstallLocalWithResult(command.Context(), home, slug, source)
			}
			if err := errors.Join(installErr, session.Close()); err != nil {
				return err
			}
			message := "已安装"
			if alreadyPresent {
				message = "已匹配"
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "%s %s\n", message, terminalText(slug))
			return err
		},
	}
	command.Flags().StringVar(&slug, "slug", "", "本地 skill 名称")
	_ = command.MarkFlagRequired("slug")
	return command
}

func localHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("home directory must be absolute")
	}
	return home, nil
}

func validateDatabasePath(directory string) (bool, error) {
	info, err := os.Lstat(filepath.Join(directory, "client.db"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect state database: %w", err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("state database must be a regular file, not a link")
	}
	return true, nil
}

func validateLocalDirectories(home, source string) (string, error) {
	resolvedSource, err := resolveTrustedDirectory(source)
	if err != nil {
		return "", fmt.Errorf("untrusted source directory: %w", err)
	}
	if err := validateTrustedDirectory(filepath.Join(home, ".agents", "skills")); err != nil {
		return "", fmt.Errorf("untrusted canonical directory: %w", err)
	}
	targets, err := install.DiscoverTargets(afero.NewOsFs(), home)
	if err != nil {
		return "", err
	}
	for _, target := range targets {
		if _, err := os.Lstat(target.SkillsDirectory); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return "", fmt.Errorf("inspect agent directory: %w", err)
		}
		if err := validateTrustedDirectory(target.SkillsDirectory); err != nil {
			return "", fmt.Errorf("untrusted %s skills directory: %w", target.Agent, err)
		}
	}
	return resolvedSource, nil
}

func newInstallStatusCommand() *cobra.Command {
	var reconcile bool
	command := &cobra.Command{
		Use:   "install-status",
		Short: "查看或保守协调未完成的本地安装",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if runtime.GOOS != "linux" {
				return fmt.Errorf("local installation is unsupported on this platform")
			}
			home, err := localHome()
			if err != nil {
				return err
			}
			directory := filepath.Join(home, ".local", "share", "skillhub")
			if _, err := os.Lstat(directory); errors.Is(err, os.ErrNotExist) {
				_, err = fmt.Fprintln(command.OutOrStdout(), "没有本地安装状态")
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
				_, err = fmt.Fprintln(command.OutOrStdout(), "没有本地安装状态")
				return err
			}
			session, err := install.OpenSession(command.Context(), directory)
			if err != nil {
				return err
			}
			var pending []state.Operation
			if reconcile {
				pending, err = session.RecoverInterruptedOperations(command.Context())
			} else {
				pending, err = session.PendingOperations(command.Context())
			}
			if err = errors.Join(err, session.Close()); err != nil {
				return err
			}
			if len(pending) == 0 {
				_, err = fmt.Fprintln(command.OutOrStdout(), "没有未完成的安装操作")
				return err
			}
			for _, entry := range pending {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "%s\t%s\t%s\n", terminalText(entry.Plan.ID), terminalText(entry.Status), terminalText(entry.Plan.Slug)); err != nil {
					return err
				}
				for _, target := range entry.Plan.Targets {
					if _, err := fmt.Fprintf(command.OutOrStdout(), "  %s\t%s\tstage=%s\tintent=%t\tpublished=%t\n", terminalText(target.Kind), terminalText(target.Path), terminalText(target.StageName), target.PublishIntent, target.Published); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
	command.Flags().BoolVar(&reconcile, "reconcile", false, "取消未开始操作并保守标记中断操作；不删除文件")
	return command
}
