//go:build !linux

package packattachments

import "context"

func readReferencesFile(context.Context, string) ([]byte, error) { return nil, ErrUnsupported }
