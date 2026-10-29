package cli

import (
	"errors"
	"fmt"
	"runtime"

	"github.com/ca-x/cognate/internal/clientkey"
	"github.com/ca-x/cognate/internal/install"
	"github.com/ca-x/cognate/internal/packclient"
	"github.com/spf13/cobra"
)

func newInstallPublicCommand() *cobra.Command {
	var origin, id string
	command := &cobra.Command{
		Use: "install-public", Short: "免 token 安装 Cognate 公开技能（仅 Linux）", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) (resultErr error) {
			if err := command.Context().Err(); err != nil {
				return err
			}
			if runtime.GOOS != "linux" {
				return clientkey.ErrUnsupported
			}
			client, err := packclient.NewPublic(origin, nil)
			if err != nil {
				return err
			}
			home, err := admitClientHome(command.Context())
			if err != nil {
				return err
			}
			entry, err := client.GetSkill(command.Context(), id)
			if err != nil {
				return err
			}
			snapshot, err := client.Download(command.Context(), entry)
			if err != nil {
				return err
			}
			defer func() { resultErr = errors.Join(resultErr, snapshot.Close()) }()
			directory, err := prepareAdmittedClientHome(command.Context(), home)
			if err != nil {
				return err
			}
			if _, err := validateLocalDirectories(home, snapshot.Path()); err != nil {
				return err
			}
			session, err := install.OpenSession(command.Context(), directory)
			if err != nil {
				return err
			}
			matched, operationErr := session.InstallLocalWithHash(command.Context(), home, entry.Slug, snapshot.Path(), entry.ContentHash)
			if err := errors.Join(operationErr, session.Close()); err != nil {
				return err
			}
			message := "已安装公开技能"
			if matched {
				message = "已匹配公开技能"
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "%s %s\n", message, terminalText(entry.Slug))
			return err
		},
	}
	command.Flags().StringVar(&origin, "server", "", "Cognate 服务端 origin（HTTPS；本地开发允许 loopback HTTP）")
	command.Flags().StringVar(&id, "skill", "", "公开目录中的 skill ID")
	_ = command.MarkFlagRequired("server")
	_ = command.MarkFlagRequired("skill")
	return command
}
