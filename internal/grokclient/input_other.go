//go:build !linux

package grokclient

import "context"

func readInputFile(context.Context, string, int64, bool) ([]byte, error) { return nil, ErrUnsupported }
