package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
)

var (
	errWatchOutputUnsupported = errors.New("watch output requires a native file, memory buffer, or cooperative WriteContext writer")
	errWatchOutputFailed      = errors.New("watch output failed")
)

type watchContextWriter interface {
	WriteContext(context.Context, []byte) (int, error)
}

type checkedWatchWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (writer checkedWatchWriter) Write(data []byte) (int, error) {
	if err := writer.ctx.Err(); err != nil {
		return 0, err
	}
	var count int
	var err error
	if output, ok := writer.writer.(watchContextWriter); ok {
		count, err = output.WriteContext(writer.ctx, data)
	} else {
		count, err = writer.writer.Write(data)
	}
	if canceled := writer.ctx.Err(); canceled != nil {
		return count, canceled
	}
	if err != nil || count != len(data) {
		return count, errWatchOutputFailed
	}
	return count, nil
}

func prepareWatchOutput(ctx context.Context, output io.Writer) (io.Writer, func() error, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if file, ok := output.(*os.File); ok {
		return prepareWatchFileOutput(ctx, file)
	}
	if output == io.Discard {
		return checkedWatchWriter{ctx: ctx, writer: output}, func() error { return nil }, nil
	}
	switch output.(type) {
	case *bytes.Buffer, *strings.Builder, watchContextWriter:
		return checkedWatchWriter{ctx: ctx, writer: output}, func() error { return nil }, nil
	default:
		return nil, nil, errWatchOutputUnsupported
	}
}
