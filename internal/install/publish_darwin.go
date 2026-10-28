package install

import (
	"os"

	"golang.org/x/sys/unix"
)

func renameNoReplace(directory *os.File, _ string, source, destination string) error {
	fd := int(directory.Fd())
	return unix.RenameatxNp(fd, source, fd, destination, unix.RENAME_EXCL)
}
