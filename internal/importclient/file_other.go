//go:build !unix && !windows

package importclient

import "os"

func openExport(string) (*os.File, error) { return nil, errFile }
