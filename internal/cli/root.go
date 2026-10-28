package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"unicode"

	"github.com/ca-x/skillhub/internal/repository"
	"github.com/ca-x/skillhub/internal/search"
	"github.com/ca-x/skillhub/internal/skill"
	"github.com/spf13/afero"
	"github.com/spf13/cobra"
)

func Execute() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	command := New()
	if err := command.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, terminalText(err.Error()))
		os.Exit(1)
	}
}

func New() *cobra.Command {
	root := &cobra.Command{Use: "skillhub", Short: "聚合与浏览 agent skills", SilenceUsage: true, SilenceErrors: true}
	root.AddCommand(newInstallLocalCommand())
	root.AddCommand(newInstallPublicCommand())
	root.AddCommand(newUninstallLocalCommand())
	root.AddCommand(newPackCommand())
	root.AddCommand(newImportCommand())
	root.AddCommand(newInstallStatusCommand())
	root.AddCommand(newBackupLocalCommand())
	root.AddCommand(newBackupListCommand())
	root.AddCommand(newRestoreLocalCommand())
	root.AddCommand(newSearchCommand(search.NewClient(nil)))
	root.AddCommand(&cobra.Command{
		Use:   "scan DIRECTORY",
		Short: "扫描本地目录中的 skills",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			root, err := filepath.EvalSymlinks(args[0])
			if err != nil {
				return fmt.Errorf("resolve scan directory: %w", err)
			}
			return showSkills(command.OutOrStdout(), root)
		},
	})
	var branch string
	browse := &cobra.Command{
		Use:   "browse OWNER/REPO",
		Short: "拉取 GitHub 仓库并浏览 skills",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			path, cleanup, err := repository.Clone(command.Context(), args[0], branch)
			if err != nil {
				return err
			}
			defer cleanup()
			return showSkills(command.OutOrStdout(), path)
		},
	}
	browse.Flags().StringVar(&branch, "branch", "", "仓库分支（默认使用仓库默认分支）")
	root.AddCommand(browse)
	return root
}

func showSkills(output io.Writer, root string) error {
	skills, err := skill.Scan(afero.NewOsFs(), root)
	if err != nil {
		return err
	}
	if len(skills) == 0 {
		_, err = fmt.Fprintln(output, "未发现 SKILL.md")
		return err
	}
	writer := tabwriter.NewWriter(output, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "NAME\tPATH\tDESCRIPTION"); err != nil {
		return err
	}
	for _, item := range skills {
		if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\n", terminalText(item.Name), terminalText(item.Path), terminalText(item.Description)); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func terminalText(text string) string {
	return strings.Map(func(character rune) rune {
		if unicode.IsControl(character) || unicode.Is(unicode.Bidi_Control, character) {
			return ' '
		}
		return character
	}, text)
}
