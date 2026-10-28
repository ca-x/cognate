package repository

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/skill"
	"github.com/spf13/afero"
)

const (
	MaxArchiveBytes  = 32 << 20
	maxExpandedBytes = 64 << 20
	maxTarBytes      = 80 << 20
	maxArchiveFiles  = 10_000
	maxSkillDocument = 1 << 20
	maxArchiveSkills = 512
)

var (
	ErrInvalidReference   = errors.New("invalid GitHub repository or branch")
	ErrArchiveUnavailable = errors.New("public GitHub archive unavailable")
	ErrArchiveInvalid     = errors.New("archive is unsafe, invalid, or contains no skills")
	ErrArchiveTooLarge    = errors.New("repository exceeds collection limits")
	ErrArchiveRateLimited = errors.New("GitHub archive download rate limited")
)

type Reference struct {
	NameWithOwner string
	Branch        string
}

type ArchivedSkill struct {
	skill.Skill
	ContentHash string
}

type ArchiveSnapshot struct {
	Data   []byte
	Hash   string
	Skills []ArchivedSkill
}

type ArchiveFetcher struct{ client *http.Client }

func ParseReference(input, branch string) (Reference, error) {
	input = strings.TrimSuffix(strings.TrimSpace(input), "/")
	if len(input) > 512 || len(branch) > 256 || !utf8.ValidString(branch) {
		return Reference{}, ErrInvalidReference
	}
	remote, err := GitHubURL(input)
	if err != nil {
		return Reference{}, ErrInvalidReference
	}
	if branch == "HEAD" {
		branch = ""
	}
	if branch != "" {
		if strings.HasPrefix(branch, "-") || strings.ContainsAny(branch, " ~^:?*[\\") || strings.Contains(branch, "..") || strings.Contains(branch, "@{") || strings.Contains(branch, "//") || strings.ContainsFunc(branch, unicode.IsControl) || branch == "@" {
			return Reference{}, ErrInvalidReference
		}
		for component := range strings.SplitSeq(branch, "/") {
			if component == "" || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".") || strings.HasSuffix(component, ".lock") {
				return Reference{}, ErrInvalidReference
			}
		}
	}
	return Reference{NameWithOwner: strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(remote, "https://github.com/"), ".git")), Branch: branch}, nil
}

func NewArchiveFetcher(client *http.Client) *ArchiveFetcher {
	transport := http.DefaultTransport
	if client != nil && client.Transport != nil {
		transport = client.Transport
	}
	return &ArchiveFetcher{client: &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (fetcher *ArchiveFetcher) Fetch(ctx context.Context, reference Reference) (*ArchiveSnapshot, error) {
	parsed, err := ParseReference(reference.NameWithOwner, reference.Branch)
	if err != nil || parsed != reference {
		return nil, ErrInvalidReference
	}
	branch := reference.Branch
	if branch == "" {
		branch = "HEAD"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://codeload.github.com/"+reference.NameWithOwner+"/tar.gz/"+url.PathEscape(branch), nil)
	if err != nil {
		return nil, ErrInvalidReference
	}
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("User-Agent", "Cognate")
	response, err := fetcher.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrArchiveUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusTooManyRequests {
		return nil, ErrArchiveRateLimited
	}
	if response.StatusCode != http.StatusOK {
		return nil, ErrArchiveUnavailable
	}
	if response.ContentLength > MaxArchiveBytes {
		return nil, ErrArchiveTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxArchiveBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrArchiveUnavailable
	}
	if len(data) > MaxArchiveBytes {
		return nil, ErrArchiveTooLarge
	}
	return inspectArchive(ctx, data)
}

func inspectArchive(ctx context.Context, data []byte) (*ArchiveSnapshot, error) {
	filesystem, err := unpackArchive(ctx, data)
	if err != nil {
		return nil, err
	}
	metadata, err := skill.Scan(afero.NewReadOnlyFs(filesystem), "/repository")
	if err != nil || len(metadata) == 0 {
		return nil, ErrArchiveInvalid
	}
	if len(metadata) > maxArchiveSkills {
		return nil, ErrArchiveTooLarge
	}
	skills := make([]ArchivedSkill, 0, len(metadata))
	for _, entry := range metadata {
		if !utf8.ValidString(entry.Name) || !utf8.ValidString(entry.Description) || len(entry.Name) > 256 || len(entry.Description) > 4096 || strings.ContainsFunc(entry.Name, unicode.IsControl) {
			return nil, ErrArchiveInvalid
		}
		contentHash, err := skill.ContentHashContext(ctx, filesystem, path.Join("/repository", entry.Path))
		if err != nil {
			return nil, err
		}
		skills = append(skills, ArchivedSkill{Skill: entry, ContentHash: contentHash})
	}
	digest := sha256.Sum256(data)
	return &ArchiveSnapshot{Data: data, Hash: hex.EncodeToString(digest[:]), Skills: skills}, nil
}

func unpackArchive(ctx context.Context, data []byte) (afero.Fs, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(data) > MaxArchiveBytes {
		return nil, ErrArchiveTooLarge
	}
	compressed, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, ErrArchiveInvalid
	}
	defer compressed.Close()
	expanded := &io.LimitedReader{R: archiveContextReader{ctx: ctx, reader: compressed}, N: maxTarBytes + 1}
	filesystem := afero.NewMemMapFs()
	if err := filesystem.Mkdir("/repository", 0o700); err != nil {
		return nil, err
	}
	if err := extractArchive(ctx, tar.NewReader(expanded), filesystem); err != nil {
		return nil, err
	}
	if err := readArchivePadding(expanded); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrArchiveInvalid
	}
	if expanded.N <= 0 {
		return nil, ErrArchiveTooLarge
	}
	return filesystem, nil
}

func extractArchive(ctx context.Context, archive *tar.Reader, filesystem afero.Fs) error {
	root := ""
	seen := make(map[string]struct{})
	var expanded int64
	for count := 0; ; count++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%w: read tar header", ErrArchiveInvalid)
		}
		if count >= maxArchiveFiles {
			return ErrArchiveTooLarge
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			if count != 0 || len(header.Xattrs) != 0 || len(header.PAXRecords) != 1 {
				return ErrArchiveInvalid
			}
			for key := range header.PAXRecords {
				if key != "comment" {
					return ErrArchiveInvalid
				}
			}
			continue
		}
		name := strings.TrimSuffix(header.Name, "/")
		if name == "" || len(name) > 1024 || !utf8.ValidString(name) || strings.Contains(name, "\\") || strings.ContainsFunc(name, unicode.IsControl) || path.IsAbs(name) || path.Clean(name) != name || strings.Count(name, "/") > 32 {
			return fmt.Errorf("%w: invalid archive path", ErrArchiveInvalid)
		}
		parts := strings.Split(name, "/")
		for _, component := range parts {
			if component == ".." || component == "." {
				return ErrArchiveInvalid
			}
		}
		if root == "" {
			root = parts[0]
		}
		if root != parts[0] {
			return fmt.Errorf("%w: multiple archive roots", ErrArchiveInvalid)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("%w: duplicate archive entry", ErrArchiveInvalid)
		}
		seen[name] = struct{}{}
		if len(parts) == 1 {
			if header.Typeflag != tar.TypeDir {
				return fmt.Errorf("%w: archive root is not a directory", ErrArchiveInvalid)
			}
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
			continue
		}
		if header.Size < 0 || header.Size > maxExpandedBytes-expanded || (path.Base(name) == "SKILL.md" && header.Size > maxSkillDocument) {
			return ErrArchiveTooLarge
		}
		expanded += header.Size
		destination := path.Join("/repository", strings.Join(parts[1:], "/"))
		if header.Typeflag == tar.TypeDir {
			if err := filesystem.MkdirAll(destination, 0o700); err != nil {
				return fmt.Errorf("%w: create archive directory", ErrArchiveInvalid)
			}
			continue
		}
		if err := filesystem.MkdirAll(path.Dir(destination), 0o700); err != nil {
			return fmt.Errorf("%w: create archive parent", ErrArchiveInvalid)
		}
		file, err := filesystem.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600|header.FileInfo().Mode().Perm()&0o111)
		if err != nil {
			return fmt.Errorf("%w: create archive file", ErrArchiveInvalid)
		}
		_, copyErr := io.Copy(file, archiveContextReader{ctx: ctx, reader: archive})
		if err := errors.Join(copyErr, file.Close()); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("%w: copy archive file", ErrArchiveInvalid)
		}
	}
}

func readArchivePadding(reader io.Reader) error {
	buffer := make([]byte, 32<<10)
	for {
		count, err := reader.Read(buffer)
		if bytes.Count(buffer[:count], []byte{0}) != count {
			return ErrArchiveInvalid
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

type archiveContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader archiveContextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}
