//go:build !unix

package logging

import "os"

func validPrivateFileInfo(os.FileInfo) bool { return false }
func privateOpenFlags(flags int) int        { return flags }
func trustedLoggingPath(string) bool        { return false }
func writeWarning(*os.File)                 {}
