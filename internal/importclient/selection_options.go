package importclient

import (
	"context"
	"io"
	"path/filepath"
	"strings"

	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func ReadSelectionOptions(ctx context.Context, path string) ([]*importv1.ImportCandidateOptions, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for part := range strings.SplitSeq(filepath.ToSlash(path), "/") {
		if part == ".." {
			return nil, errSelection
		}
	}
	file, err := openExport(path)
	if err != nil {
		return nil, errSelection
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 64<<10 {
		return nil, errSelection
	}
	data, err := io.ReadAll(io.LimitReader(file, (64<<10)+1))
	defer clear(data)
	if err != nil || len(data) > 64<<10 {
		return nil, errSelection
	}
	var request importv1.PreviewImportSelectionRequest
	if err := (protojson.UnmarshalOptions{RecursionLimit: 32}).Unmarshal(data, &request); err != nil || request.Source != 0 || len(request.SourceData) != 0 || len(request.Options) > 160 {
		return nil, errSelection
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return request.Options, nil
}
