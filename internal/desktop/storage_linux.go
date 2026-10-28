package desktop

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
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

func clientKey(home string, create bool) ([]byte, error) {
	directory := filepath.Join(home, ".config", "skillhub")
	if err := trustedDirectory(directory, true); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	before, err := root.Lstat("client.key")
	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		return nil, err
	}
	if missing && !create {
		return nil, errors.New("请恢复既有 client.key，不能为已有订阅生成新密钥")
	}
	if !missing && !privateKey(before) {
		return nil, errors.New("client.key 必须为私有、无硬链接的常规文件")
	}
	flags := os.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK
	if missing {
		flags = os.O_RDWR | os.O_CREATE | os.O_EXCL | syscall.O_NOFOLLOW
	}
	file, err := root.OpenFile("client.key", flags, 0600)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !privateKey(info) || !missing && !os.SameFile(before, info) {
		return nil, errors.New("client.key 身份变化")
	}
	if missing {
		key := make([]byte, 32)
		defer clear(key)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		if _, err = file.Write(key); err != nil {
			return nil, err
		}
		if err = file.Sync(); err != nil {
			return nil, err
		}
		parent, err := root.Open(".")
		if err != nil {
			return nil, err
		}
		if err = errors.Join(parent.Sync(), parent.Close()); err != nil {
			return nil, err
		}
		if _, err = file.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
	}
	key, err := io.ReadAll(io.LimitReader(file, 33))
	after, afterErr := root.Lstat("client.key")
	if err != nil || len(key) != 32 || afterErr != nil || !privateKey(after) || !os.SameFile(info, after) {
		clear(key)
		return nil, fmt.Errorf("读取 client.key 失败")
	}
	return key, nil
}

func privateKey(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	attributes, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && info.Mode().Perm()&0077 == 0 && attributes.Uid == uint32(os.Geteuid()) && attributes.Nlink == 1
}
