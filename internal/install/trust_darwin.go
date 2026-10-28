package install

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

func validateTrustedTree(ctx context.Context, directory string, includeGit bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return fmt.Errorf("open skill directory: %w", err)
	}
	defer root.Close()
	return validateTrustedRoot(ctx, root, includeGit)
}

func validateTrustedRoot(ctx context.Context, root *os.Root, includeGit bool) error {
	return fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		info, err := root.Lstat(path)
		if err != nil {
			return err
		}
		if info.IsDir() != entry.IsDir() {
			return fmt.Errorf("skill entry changed during inspection")
		}
		if !includeGit && info.IsDir() && info.Name() == ".git" && path != "." {
			return fs.SkipDir
		}
		attributes, ok := info.Sys().(*syscall.Stat_t)
		if !ok || attributes.Uid != uint32(os.Geteuid()) && attributes.Uid != 0 || info.Mode().Perm()&0o022 != 0 || !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("skill entry is not trusted: %s", path)
		}
		return nil
	})
}

func validatePrivateDirectory(info os.FileInfo) error {
	attributes, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || attributes.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("backup directory must be private and owned by the current user")
	}
	return nil
}
