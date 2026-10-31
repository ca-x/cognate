package servercli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ca-x/cognate/internal/server"
	"github.com/spf13/cobra"
)

func Execute() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := New().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func New() *cobra.Command {
	var config server.Config
	config.SourceRefreshInterval = new(15 * time.Minute)
	var listen string
	command := &cobra.Command{
		Use: "cognate-server", Short: "Cognate 服务端", Args: cobra.NoArgs,
		SilenceUsage: true, SilenceErrors: true,
		RunE: func(command *cobra.Command, _ []string) error {
			if config.DataDirectory == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return fmt.Errorf("cannot determine server data directory")
				}
				config.DataDirectory = filepath.Join(home, ".local", "share", "skillhub-server")
			}
			app, err := server.Open(command.Context(), config)
			if err != nil {
				return err
			}
			listener, err := net.Listen("tcp", listen)
			if err != nil {
				return errors.Join(fmt.Errorf("cannot listen on configured address"), app.Close())
			}
			return errors.Join(app.Serve(command.Context(), listener), app.Close())
		},
	}
	command.Flags().StringVar(&config.DataDirectory, "data-dir", "", "服务端私有数据目录，默认 ~/.local/share/skillhub-server")
	command.Flags().StringArrayVar(&config.AdminUserIDs, "admin-user-id", nil, "允许管理全局日志设置的账号 UUID；可重复，默认无人有权限（后台日志页可查看自己的账号 ID）")
	command.Flags().StringVar(&config.BootstrapAdminUsername, "bootstrap-admin-username", "", "仅空实例创建的首位本地管理员用户名；重启须保持一致，不自动提升已有账号")
	command.Flags().StringVar(&config.BootstrapAdminPasswordFile, "bootstrap-admin-password-file", "", "首次管理员密码的私有文件（8–1024 字节，原样读取）；重启不读取、不覆盖已有密码")
	command.Flags().StringVar(&config.MasterKeyFile, "key-file", configurationEnvironment("MASTER_KEY_FILE"), "包含 32 字节原始主密钥的私有文件，或 COGNATE_MASTER_KEY_FILE（兼容 SKILLHUB_MASTER_KEY_FILE）")
	command.Flags().StringVar(&listen, "listen", "127.0.0.1:8080", "HTTP 监听地址")
	command.Flags().StringVar(&config.TLSCertificate, "tls-cert", "", "TLS 证书文件")
	command.Flags().StringVar(&config.TLSKey, "tls-key", "", "TLS 私钥文件")
	command.Flags().BoolVar(&config.AllowHTTP, "allow-http", false, "允许在可信私有网络监听非 loopback HTTP（例如 TLS 反向代理后方）")
	command.Flags().StringVar(&config.PublicOrigin, "public-origin", "", "浏览器访问的外部 origin，例如 https://skills.example.com")
	command.Flags().StringVar(&config.GitHubClientID, "github-client-id", configurationEnvironment("GITHUB_CLIENT_ID"), "启用 device flow 的 GitHub OAuth App client ID")
	command.Flags().StringVar(&config.OIDCIssuer, "oidc-issuer", configurationEnvironment("OIDC_ISSUER"), "OpenID Connect issuer URL（启用 discovery）")
	command.Flags().StringVar(&config.OIDCClientID, "oidc-client-id", configurationEnvironment("OIDC_CLIENT_ID"), "OpenID Connect client ID")
	command.Flags().StringVar(&config.OIDCClientSecretFile, "oidc-client-secret-file", configurationEnvironment("OIDC_CLIENT_SECRET_FILE"), "保存 OIDC client secret 的私有文件（公开客户端可省略）")
	command.Flags().Int64Var(&config.ArchiveCacheBytes, "archive-cache-bytes", 0, "初始化归档缓存字节预算，0 使用 512 MiB；须与同目录已保存策略一致（包括中断残留）")
	command.Flags().DurationVar(&config.ListSyncInterval, "list-sync-interval", 15*time.Minute, "订阅 GitHub Lists 同步间隔，0 禁用；只自动收集公开仓库，不替换已保存内容")
	command.Flags().DurationVar(config.SourceRefreshInterval, "source-refresh-interval", *config.SourceRefreshInterval, "已收录公开来源的更新批次间隔，0 禁用；非全源刷新时限，已公开快照内容变化仍需取消公开后更新")
	return command
}

func configurationEnvironment(suffix string) string {
	if value, present := os.LookupEnv("COGNATE_" + suffix); present {
		return value
	}
	return os.Getenv("SKILLHUB_" + suffix)
}
