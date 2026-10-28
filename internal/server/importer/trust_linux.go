package importer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

func validateDatabaseTrust(path string, info os.FileInfo) error {
	attributes, ok := info.Sys().(*syscall.Stat_t)
	if !ok || attributes.Uid != uint32(os.Geteuid()) && attributes.Uid != 0 || info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("cc-switch database is not trusted")
	}
	parent := filepath.Dir(path)
	current := string(filepath.Separator)
	components := strings.Split(strings.TrimPrefix(parent, current), current)
	for index := -1; index < len(components); index++ {
		if index >= 0 {
			if components[index] == "" {
				continue
			}
			current = filepath.Join(current, components[index])
		}
		entry, err := os.Lstat(current)
		if err != nil || !entry.IsDir() || entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("cc-switch database directory is not trusted")
		}
		attributes, ok := entry.Sys().(*syscall.Stat_t)
		if !ok || attributes.Uid != uint32(os.Geteuid()) && attributes.Uid != 0 {
			return fmt.Errorf("cc-switch database directory is not trusted")
		}
		if entry.Mode().Perm()&0o022 != 0 && (current == parent || entry.Mode()&os.ModeSticky == 0) {
			return fmt.Errorf("cc-switch database directory is writable by other users")
		}
	}
	return nil
}
