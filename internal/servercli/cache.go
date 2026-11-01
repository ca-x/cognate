package servercli

import (
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/runtimegen"
	"github.com/spf13/cobra"
)

func cacheCommand() *cobra.Command {
	group := &cobra.Command{Use: "cache", Short: "离线归档缓存维护（仅 Linux）", Args: cobra.NoArgs}
	var directory, hash string
	verify := &cobra.Command{
		Use: "verify", Short: "停止服务后校验指定归档，不修改内容", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			digest, err := hex.DecodeString(hash)
			if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || err != nil || len(digest) != 32 || hex.EncodeToString(digest) != hash {
				return fmt.Errorf("explicit absolute data directory and lowercase SHA256 are required")
			}
			owner, err := runtimegen.LockOffline(command.Context(), directory)
			if err != nil {
				return err
			}
			if err := errors.Join(library.VerifyOfflineArchive(command.Context(), owner, hash), owner.Close()); err != nil {
				return err
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), "archive verified")
			return err
		},
	}
	verify.Flags().StringVar(&directory, "data-dir", "", "必须显式指定的服务端绝对数据目录")
	verify.Flags().StringVar(&hash, "sha256", "", "必须显式指定的小写归档 SHA256")
	group.AddCommand(verify)
	return group
}
