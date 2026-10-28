package cli

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func loadClientKey(home string, allowCreate bool) (result []byte, resultErr error) {
	directory := filepath.Join(home, ".config", "skillhub")
	if allowCreate {
		if err := createTrustedDirectory(directory, true); err != nil {
			return nil, err
		}
	} else if err := validateStateDirectory(directory, false); err != nil {
		return nil, fmt.Errorf("restore the existing private client credential key before accessing subscriptions")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("open private client key directory")
	}
	defer func() {
		resultErr = errors.Join(resultErr, root.Close())
		if resultErr != nil {
			clear(result)
			result = nil
		}
	}()
	before, err := root.Lstat("client.key")
	created := errors.Is(err, os.ErrNotExist)
	if created && !allowCreate {
		return nil, fmt.Errorf("restore the existing client credential key; saved subscriptions cannot use a new key")
	}
	if err != nil && !created {
		return nil, fmt.Errorf("inspect client credential key")
	}
	if !created && !trustedClientKey(before) {
		return nil, fmt.Errorf("client credential key must be private, owned and not linked")
	}
	flags := os.O_RDONLY
	if created {
		flags = os.O_RDWR | os.O_CREATE | os.O_EXCL
	}
	file, err := root.OpenFile("client.key", flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open private client credential key")
	}
	defer func() {
		resultErr = errors.Join(resultErr, file.Close())
		if resultErr != nil {
			clear(result)
			result = nil
		}
	}()
	opened, err := file.Stat()
	if err != nil || !trustedClientKey(opened) || before != nil && !os.SameFile(before, opened) {
		return nil, fmt.Errorf("client credential key must be private, owned and not linked")
	}
	if created {
		key := make([]byte, 32)
		defer clear(key)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate client credential key")
		}
		if _, err := file.Write(key); err != nil {
			return nil, fmt.Errorf("write client credential key")
		}
		if err := file.Sync(); err != nil {
			return nil, fmt.Errorf("sync client credential key")
		}
		parent, err := root.Open(".")
		if err != nil {
			return nil, err
		}
		if err := errors.Join(parent.Sync(), parent.Close()); err != nil {
			return nil, fmt.Errorf("sync client credential key directory")
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return nil, fmt.Errorf("read new client credential key")
		}
	}
	data, err := io.ReadAll(io.LimitReader(file, 33))
	if err != nil || len(data) != 32 {
		clear(data)
		return nil, fmt.Errorf("client credential key must contain exactly 32 bytes")
	}
	after, err := root.Lstat("client.key")
	if err != nil || !trustedClientKey(after) || !os.SameFile(opened, after) {
		clear(data)
		return nil, fmt.Errorf("client credential key changed during read")
	}
	return data, nil
}

func trustedClientKey(info os.FileInfo) bool {
	if info == nil {
		return false
	}
	attributes, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && info.Mode().Perm()&0o077 == 0 && attributes.Uid == uint32(os.Geteuid()) && attributes.Nlink == 1
}
