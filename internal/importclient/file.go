package importclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"path/filepath"
	"strings"
)

const MaxExportBytes = 1 << 20

var errFile = errors.New("import requires a nonempty regular file of at most 1 MiB without symbolic links")

func readExport(ctx context.Context, path string) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	for component := range strings.SplitSeq(filepath.ToSlash(path), "/") {
		if component == ".." {
			return nil, "", errFile
		}
	}
	file, err := openExport(path)
	if err != nil {
		return nil, "", errFile
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxExportBytes {
		return nil, "", errFile
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxExportBytes+1))
	if err != nil || len(data) == 0 || len(data) > MaxExportBytes {
		clear(data)
		return nil, "", errFile
	}
	if err := ctx.Err(); err != nil {
		clear(data)
		return nil, "", err
	}
	digest := sha256.Sum256(data)
	return data, hex.EncodeToString(digest[:]), nil
}
