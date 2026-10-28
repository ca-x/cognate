package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func prepareStateDirectory(path string) error {
	if err := createTrustedDirectory(path, true); err != nil {
		return err
	}
	return validateStateDirectory(path, false)
}

func createTrustedDirectory(path string, private bool) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("directory path must be absolute")
	}
	path = filepath.Clean(path)
	root, err := os.OpenRoot(string(filepath.Separator))
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Stat(".")
	if err != nil {
		return err
	}
	if err := trustedDirectoryInfo(info, false, false); err != nil {
		return err
	}
	components := strings.Split(strings.TrimPrefix(path, string(filepath.Separator)), string(filepath.Separator))
	for index, component := range components {
		if component == "" {
			continue
		}
		info, err = root.Lstat(component)
		if errors.Is(err, fs.ErrNotExist) {
			if err := root.Mkdir(component, 0o700); err != nil {
				return fmt.Errorf("create directory component: %w", err)
			}
			info, err = root.Lstat(component)
		}
		if err != nil {
			return fmt.Errorf("inspect directory component: %w", err)
		}
		leaf := index == len(components)-1
		if err := trustedDirectoryInfo(info, leaf, private); err != nil {
			return err
		}
		child, err := root.OpenRoot(component)
		if err != nil {
			return fmt.Errorf("open directory component: %w", err)
		}
		opened, err := child.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			_ = child.Close()
			return fmt.Errorf("directory component changed during creation")
		}
		if err := root.Close(); err != nil {
			_ = child.Close()
			return err
		}
		root = child
	}
	return nil
}

func trustedDirectoryInfo(info os.FileInfo, leaf, private bool) error {
	attributes, ok := info.Sys().(*syscall.Stat_t)
	user := uint32(os.Geteuid())
	if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || attributes.Uid != user && attributes.Uid != 0 {
		return fmt.Errorf("directory path is not trusted")
	}
	if leaf && private {
		if attributes.Uid != user || info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("directory must be private and owned by the current user")
		}
	} else if info.Mode().Perm()&0o022 != 0 && (leaf || info.Mode()&os.ModeSticky == 0) {
		return fmt.Errorf("directory is writable by other users")
	}
	return nil
}

func validateTrustedDirectory(path string) error {
	_, err := resolveTrustedDirectory(path)
	return err
}

func resolveTrustedDirectory(path string) (string, error) {
	absolute := path
	if !filepath.IsAbs(path) {
		workingDirectory, err := os.Getwd()
		if err != nil {
			return "", err
		}
		workingDirectory, err = filepath.EvalSymlinks(workingDirectory)
		if err != nil {
			return "", err
		}
		absolute = workingDirectory + string(filepath.Separator) + path
	}
	current := string(filepath.Separator)
	root, err := os.Lstat(current)
	if err != nil {
		return "", err
	}
	if err := trustedDirectoryInfo(root, false, false); err != nil {
		return "", err
	}
	remaining := strings.Split(strings.TrimPrefix(absolute, string(filepath.Separator)), string(filepath.Separator))
	links := 0
	for len(remaining) > 0 {
		component := remaining[0]
		remaining = remaining[1:]
		if component == "" || component == "." {
			continue
		}
		if component == ".." {
			current = filepath.Dir(current)
			continue
		}
		candidate := filepath.Join(current, component)
		info, err := os.Lstat(candidate)
		if err != nil {
			return "", fmt.Errorf("inspect trusted directory: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			links++
			if links > 40 {
				return "", fmt.Errorf("too many directory symlinks")
			}
			attributes, ok := info.Sys().(*syscall.Stat_t)
			if !ok || attributes.Uid != uint32(os.Geteuid()) && attributes.Uid != 0 {
				return "", fmt.Errorf("directory symlink is not trusted")
			}
			target, err := os.Readlink(candidate)
			if err != nil {
				return "", fmt.Errorf("resolve directory symlink: %w", err)
			}
			if filepath.IsAbs(target) {
				current = string(filepath.Separator)
			}
			remaining = append(strings.Split(target, string(filepath.Separator)), remaining...)
			continue
		}
		if err := trustedDirectoryInfo(info, false, false); err != nil {
			return "", err
		}
		current = candidate
	}
	info, err := os.Lstat(current)
	if err != nil {
		return "", fmt.Errorf("inspect trusted directory: %w", err)
	}
	if err := trustedDirectoryInfo(info, true, false); err != nil {
		return "", err
	}
	return current, nil
}

func validateStateDirectory(path string, allowMissing bool) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("state directory must be absolute")
	}
	path = filepath.Clean(path)
	user := uint32(os.Geteuid())
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if errors.Is(err, fs.ErrNotExist) && allowMissing {
			if filepath.Dir(current) == current {
				return fmt.Errorf("state directory has no existing ancestor")
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect state directory path: %w", err)
		}
		attributes, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || attributes.Uid != user && attributes.Uid != 0 {
			return fmt.Errorf("state directory path is not trusted: %s", current)
		}
		if current == path {
			if attributes.Uid != user || info.Mode().Perm()&0o077 != 0 {
				return fmt.Errorf("state directory must be private and owned by the current user")
			}
		} else if info.Mode().Perm()&0o022 != 0 && info.Mode()&os.ModeSticky == 0 {
			return fmt.Errorf("state directory ancestor is writable by other users: %s", current)
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}
