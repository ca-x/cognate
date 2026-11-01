package servercli

import (
	"crypto/rand"
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
	group.AddCommand(cacheRepairCommand())
	return group
}

func cacheRepairCommand() *cobra.Command {
	var directory, hash, input, repairID string
	command := &cobra.Command{
		Use: "repair", Short: "使用显式本机归档修复缓存，保留损坏原件（仅 Linux）", Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			digest, err := hex.DecodeString(hash)
			if !filepath.IsAbs(directory) || filepath.Clean(directory) != directory || err != nil || len(digest) != 32 || hex.EncodeToString(digest) != hash {
				return fmt.Errorf("explicit absolute data directory and lowercase SHA256 are required")
			}
			if repairID == "" {
				var random [16]byte
				if _, err := rand.Read(random[:]); err != nil {
					return fmt.Errorf("generate repair ID")
				}
				repairID = hex.EncodeToString(random[:])
			}
			identifier, err := hex.DecodeString(repairID)
			if err != nil || len(identifier) != 16 || hex.EncodeToString(identifier) != repairID {
				return fmt.Errorf("repair ID must contain exactly 32 lowercase hexadecimal characters")
			}
			data, err := library.ReadArchiveRepairInput(command.Context(), input)
			if err != nil {
				return err
			}
			defer clear(data)
			if _, err := fmt.Fprintln(command.OutOrStdout(), "archive repair started; repair-id="+repairID); err != nil {
				return err
			}
			owner, err := runtimegen.LockOffline(command.Context(), directory)
			if err != nil {
				return err
			}
			outcome, repairError := library.RepairOfflineArchive(command.Context(), owner, hash, repairID, data)
			if closeError := owner.Close(); closeError != nil {
				return errors.Join(library.ErrArchiveRepairUncertain, repairError, closeError)
			}
			if repairError != nil {
				return repairError
			}
			status := "archive repaired"
			if outcome.AlreadyVerified {
				status = "archive already verified"
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "%s; retained-artifact=%t\n", status, outcome.Retained)
			if err != nil && !outcome.AlreadyVerified {
				return errors.Join(library.ErrArchiveRepairUncertain, err)
			}
			return err
		},
	}
	command.Flags().StringVar(&directory, "data-dir", "", "必须显式指定的服务端绝对数据目录")
	command.Flags().StringVar(&hash, "sha256", "", "必须显式指定的小写归档 SHA256")
	command.Flags().StringVar(&input, "archive-file", "", "私有本机归档文件的绝对路径（最大 32 MiB）")
	command.Flags().StringVar(&repairID, "repair-id", "", "显式续接 ID（32 位小写 hex）；省略则随机生成")
	return command
}
