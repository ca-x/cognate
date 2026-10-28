package cli

import (
	"context"
	"fmt"
	"text/tabwriter"

	"github.com/ca-x/cognate/internal/search"
	"github.com/spf13/cobra"
)

type skillSearcher interface {
	Search(context.Context, string, int) ([]search.Result, error)
}

func newSearchCommand(client skillSearcher) *cobra.Command {
	var limit int
	command := &cobra.Command{
		Use:   "search QUERY",
		Short: "通过 skills.sh 搜索公开 skills",
		Args:  cobra.ExactArgs(1),
		RunE: func(command *cobra.Command, args []string) error {
			results, err := client.Search(command.Context(), args[0], limit)
			if err != nil {
				return err
			}
			if len(results) == 0 {
				_, err := fmt.Fprintln(command.OutOrStdout(), "未发现匹配的 skills")
				return err
			}
			writer := tabwriter.NewWriter(command.OutOrStdout(), 0, 4, 2, ' ', 0)
			if _, err := fmt.Fprintln(writer, "NAME\tSOURCE\tSKILL ID\tINSTALLS"); err != nil {
				return err
			}
			for _, result := range results {
				if _, err := fmt.Fprintf(writer, "%s\t%s\t%s\t%d\n", terminalText(result.Name), terminalText(result.Source), terminalText(result.SkillID), result.Installs); err != nil {
					return err
				}
			}
			return writer.Flush()
		},
	}
	command.Flags().IntVar(&limit, "limit", 20, "最多返回的结果数（1–100）")
	return command
}
