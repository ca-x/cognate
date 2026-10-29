package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func trustedDirectory(path string, private bool) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("本地目录必须是干净的绝对路径")
	}
	root, err := os.OpenRoot("/")
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	components := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for index, component := range components {
		info, err := root.Lstat(component)
		if errors.Is(err, os.ErrNotExist) {
			if err = root.Mkdir(component, 0700); err != nil {
				return err
			}
			info, err = root.Lstat(component)
		}
		if err != nil {
			return err
		}
		attributes, ok := info.Sys().(*syscall.Stat_t)
		leaf := index == len(components)-1
		if !ok || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || attributes.Uid != uint32(os.Geteuid()) && attributes.Uid != 0 || info.Mode().Perm()&0022 != 0 && (leaf || info.Mode()&os.ModeSticky == 0) {
			return errors.New("本地目录不可信（链接、所有者或权限）")
		}
		if leaf && private && (attributes.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0077 != 0) {
			return errors.New("状态目录必须私有且属于当前用户")
		}
		child, err := root.OpenRoot(component)
		if err != nil {
			return err
		}
		opened, err := child.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			_ = child.Close()
			return errors.New("目录在打开时发生变化")
		}
		_ = root.Close()
		root = child
	}
	return nil
}

func prepareHome(home string) (string, error) {
	directory := filepath.Join(home, ".local", "share", "skillhub")
	if err := trustedDirectory(directory, true); err != nil {
		return "", err
	}
	if err := trustedDirectory(filepath.Join(home, ".agents", "skills"), false); err != nil {
		return "", err
	}
	return directory, nil
}
