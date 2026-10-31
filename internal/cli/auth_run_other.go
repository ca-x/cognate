//go:build !linux

package cli

import (
	"context"
	"io"
)

func authPlatform() error                                                     { return errAuthRun }
func authCredentials(context.Context, string, *authPrivate) error             { return errAuthRun }
func authChild(context.Context, []string, []byte, io.Writer, io.Writer) error { return errAuthRun }
