//go:build !unix

package githubproxysettings

import "os"

func privateFileInfo(os.FileInfo) bool { return false }
