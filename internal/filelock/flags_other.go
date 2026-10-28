//go:build !unix

package filelock

import "os"

func lockOpenFlags() int {
	return os.O_CREATE | os.O_RDWR
}
