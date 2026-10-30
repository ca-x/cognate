//go:build !linux

package secretservice

import "context"

func Read(ctx context.Context, key Key) Result { return Result{} }
func serveProcess() int                        { return 2 }
