package packclient

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/skill"
	"github.com/spf13/afero"
)

const (
	maxTarBytes     = 80 << 20
	maxFileBytes    = 64 << 20
	maxEntries      = 10_000
	maxSkillDocSize = 1 << 20
	tarBlockBytes   = 512
)

func extractSkill(ctx context.Context, source io.Reader, directory, expectedHash string) error {
	buffered := bufio.NewReader(source)
	compressed, err := gzip.NewReader(buffered)
	if err != nil {
		return errArchive
	}
	defer compressed.Close()
	compressed.Multistream(false)
	if err := os.Mkdir(directory, 0o700); err != nil {
		return errLocal
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return errLocal
	}
	defer root.Close()
	expanded := &io.LimitedReader{R: contextReader{ctx: ctx, source: compressed}, N: maxTarBytes + 1}
	if err := extractEntries(ctx, expanded, root); err != nil {
		return err
	}
	if err := readPadding(expanded); err != nil {
		return errArchive
	}
	if expanded.N <= 0 {
		return errLimit
	}
	if _, err := buffered.ReadByte(); !errors.Is(err, io.EOF) {
		return errArchive
	}
	actual, err := skill.ContentHashContext(ctx, afero.NewOsFs(), directory)
	if err != nil {
		return errArchive
	}
	if actual != expectedHash {
		return errArchive
	}
	return nil
}

func extractEntries(ctx context.Context, expanded *io.LimitedReader, root *os.Root) error {
	records := &tarRecordReader{source: expanded}
	archive := tar.NewReader(records)
	seen := make(map[string]struct{})
	portable := make(map[string]string)
	var total int64
	padding := 0
	hasDocument := false
	for count := 0; ; count++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		records.count = 0
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			if !records.terminated(padding) || count == 0 || !hasDocument {
				return errArchive
			}
			return nil
		}
		if err != nil {
			return errArchive
		}
		if count >= maxEntries || expanded.N <= 0 {
			return errLimit
		}
		name, err := archiveName(header, portable)
		if err != nil {
			return err
		}
		if _, duplicate := seen[name]; duplicate {
			return errArchive
		}
		seen[name] = struct{}{}
		if count == 0 && (name != "skill" || header.Typeflag != tar.TypeDir) || name == "skill" && header.Typeflag != tar.TypeDir {
			return errArchive
		}
		if header.Size < 0 || header.Size > maxFileBytes-total || path.Base(name) == "SKILL.md" && header.Size > maxSkillDocSize {
			return errLimit
		}
		total += header.Size
		padding = int((tarBlockBytes - header.Size%tarBlockBytes) % tarBlockBytes)
		if name == "skill" {
			continue
		}
		relative := strings.TrimPrefix(name, "skill/")
		if header.Typeflag == tar.TypeDir {
			if err := root.MkdirAll(relative, 0o700); err != nil {
				return errArchive
			}
			continue
		}
		if err := extractFile(ctx, archive, root, relative, header); err != nil {
			return err
		}
		if relative == "SKILL.md" {
			hasDocument = true
		}
	}
}

type tarRecordReader struct {
	source io.Reader
	count  int
	prefix [3 * tarBlockBytes]byte
}

func (reader *tarRecordReader) Read(buffer []byte) (int, error) {
	count, err := reader.source.Read(buffer)
	if reader.count < len(reader.prefix) {
		copy(reader.prefix[reader.count:], buffer[:count])
	}
	reader.count += count
	return count, err
}

func (reader *tarRecordReader) terminated(padding int) bool {
	end := padding + 2*tarBlockBytes
	return reader.count == end && bytes.Count(reader.prefix[padding:end], []byte{0}) == 2*tarBlockBytes
}

func archiveName(header *tar.Header, portable map[string]string) (string, error) {
	name := strings.TrimSuffix(header.Name, "/")
	if name == "" || len(name) > 1024 || !utf8.ValidString(name) || path.Clean(name) != name || strings.Count(name, "/") > 32 || name != "skill" && !strings.HasPrefix(name, "skill/") {
		return "", errArchive
	}
	if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir || header.Linkname != "" || header.Mode < 0 || header.Mode&^0o777 != 0 || header.Typeflag == tar.TypeDir && header.Size != 0 || header.Typeflag == tar.TypeReg && strings.HasSuffix(header.Name, "/") || len(header.Xattrs) != 0 {
		return "", errArchive
	}
	for key, value := range header.PAXRecords {
		if key != "path" || value != header.Name {
			return "", errArchive
		}
	}
	current := ""
	for component := range strings.SplitSeq(name, "/") {
		if !portableComponent(component) {
			return "", errArchive
		}
		current = path.Join(current, component)
		folded := strings.ToLower(current)
		if existing, ok := portable[folded]; ok && existing != current {
			return "", errArchive
		}
		portable[folded] = current
		if len(portable) > maxEntries {
			return "", errLimit
		}
	}
	return name, nil
}

func portableComponent(component string) bool {
	if component == "" || component == "." || component == ".." || strings.ContainsAny(component, "\\:*?\"<>|") || strings.ContainsFunc(component, unicode.IsControl) || strings.HasSuffix(component, " ") || strings.HasSuffix(component, ".") {
		return false
	}
	lowercase := strings.ToLower(component)
	if lowercase == ".git" {
		return false
	}
	base, _, _ := strings.Cut(lowercase, ".")
	if base == "con" || base == "prn" || base == "aux" || base == "nul" {
		return false
	}
	return !(len(base) == 4 && (strings.HasPrefix(base, "com") || strings.HasPrefix(base, "lpt")) && base[3] >= '1' && base[3] <= '9')
}

func extractFile(ctx context.Context, source io.Reader, root *os.Root, name string, header *tar.Header) error {
	if err := root.MkdirAll(path.Dir(name), 0o700); err != nil {
		return errArchive
	}
	mode := os.FileMode(0o600 | header.Mode&0o111)
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return errArchive
	}
	written, copyErr := io.Copy(file, contextReader{ctx: ctx, source: source})
	modeErr := file.Chmod(mode)
	closeErr := file.Close()
	if errors.Join(copyErr, modeErr, closeErr) != nil || written != header.Size {
		return errArchive
	}
	return nil
}

func readPadding(reader io.Reader) error {
	buffer := make([]byte, 32<<10)
	for {
		count, err := reader.Read(buffer)
		if bytes.Count(buffer[:count], []byte{0}) != count {
			return errArchive
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (reader contextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.source.Read(buffer)
}
