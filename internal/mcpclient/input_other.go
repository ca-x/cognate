//go:build !linux

package mcpclient

import "context"

func readInputFile(context.Context, string, int64, bool) ([]byte, error) { return nil, ErrInvalid }
