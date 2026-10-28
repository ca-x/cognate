package importclient

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"unicode"

	"connectrpc.com/connect"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/gen/skillhub/import/v1/importv1connect"
	libraryv1 "github.com/ca-x/cognate/gen/skillhub/library/v1"
	"github.com/ca-x/cognate/gen/skillhub/library/v1/libraryv1connect"
	"github.com/ca-x/cognate/internal/repository"
)

var (
	errSession    = errors.New("invalid bounded session credential")
	errPreview    = errors.New("invalid import preview response")
	errSelection  = errors.New("select unique candidate numbers from this client's preview")
	errHash       = errors.New("export SHA256 differs from the expected preview; preview again before applying")
	ErrIncomplete = errors.New("import incomplete; completed entries remain, unknown entries may have committed; no automatic retry or rollback")
)

type Client struct {
	preview importv1connect.ImportServiceClient
	library libraryv1connect.LibraryServiceClient
	session string
	slot    chan struct{}
}

func (Client) String() string       { return "[redacted import client]" }
func (Client) GoString() string     { return "[redacted import client]" }
func (Client) LogValue() slog.Value { return slog.StringValue("[redacted import client]") }
func (Client) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[redacted import client]")
}

func ReadSession(input io.Reader) (string, error) {
	if input == nil {
		return "", errSession
	}
	data, err := io.ReadAll(io.LimitReader(input, 2049))
	defer clear(data)
	if err != nil || len(data) > 2048 {
		return "", errSession
	}
	session := string(bytes.TrimSpace(data))
	if !validSession(session) {
		return "", errSession
	}
	return session, nil
}

func validSession(session string) bool {
	if len(session) != 87 || session[43] != '.' {
		return false
	}
	for index, character := range session {
		if index == 43 {
			continue
		}
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-') {
			return false
		}
	}
	return true
}

func New(server, session string, supplied *http.Client) (*Client, error) {
	origin, err := parseOrigin(server)
	if err != nil {
		return nil, err
	}
	if !validSession(session) {
		return nil, errSession
	}
	bounded := boundedHTTPClient(origin, supplied)
	return &Client{
		preview: importv1connect.NewImportServiceClient(bounded, origin.String(), connect.WithReadMaxBytes(maxResponseBytes), connect.WithSendMaxBytes(2<<20)),
		library: libraryv1connect.NewLibraryServiceClient(bounded, origin.String(), connect.WithReadMaxBytes(maxResponseBytes), connect.WithSendMaxBytes(2048)),
		session: session, slot: make(chan struct{}, 1),
	}, nil
}

type Candidate struct {
	Index      int
	Repository string
	Branch     string
	Existing   bool
}

type Preview struct {
	client           *Client
	sha256           string
	candidates       []Candidate
	referencedSkills uint32
}

func (preview *Preview) SHA256() string           { return preview.sha256 }
func (preview *Preview) Candidates() []Candidate  { return slices.Clone(preview.candidates) }
func (preview *Preview) ReferencedSkills() uint32 { return preview.referencedSkills }

func (client *Client) acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if client == nil || client.slot == nil {
		return nil, errors.New("import client is not initialized")
	}
	select {
	case client.slot <- struct{}{}:
		return func() { <-client.slot }, nil
	default:
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("import client is busy"))
	}
}

func (client *Client) PreviewFile(ctx context.Context, path string) (*Preview, error) {
	return client.previewFile(ctx, path, "")
}

func (client *Client) PreviewFileAtHash(ctx context.Context, path, expectedSHA256 string) (*Preview, error) {
	if !validHash(expectedSHA256) {
		return nil, errHash
	}
	return client.previewFile(ctx, path, expectedSHA256)
}

func (client *Client) previewFile(ctx context.Context, path, expectedSHA256 string) (*Preview, error) {
	release, err := client.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	data, digest, err := readExport(ctx, path)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	if expectedSHA256 != "" && digest != expectedSHA256 {
		return nil, errHash
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request := connect.NewRequest(&importv1.PreviewLibraryExportRequest{Format: importv1.ExportFormat_EXPORT_FORMAT_MAGPIE_LIBRARY_JSON, ExportData: data})
	request.Header().Set("Authorization", "Bearer "+client.session)
	response, err := client.preview.PreviewLibraryExport(ctx, request)
	if err != nil {
		return nil, requestError(ctx, err)
	}
	candidates, err := validatePreview(response.Msg)
	if err != nil {
		return nil, err
	}
	return &Preview{client: client, sha256: digest, candidates: candidates, referencedSkills: response.Msg.ReferencedSkillCount}, nil
}

func validatePreview(response *importv1.PreviewLibraryExportResponse) ([]Candidate, error) {
	if response == nil || response.ReferencedSkillCount > 1000 || len(response.Repositories) > int(response.ReferencedSkillCount) {
		return nil, errPreview
	}
	candidates := make([]Candidate, 0, len(response.Repositories))
	seen := make(map[repository.Reference]bool)
	for index, entry := range response.Repositories {
		if entry == nil || strings.Count(entry.Repository, "/") != 1 || strings.ContainsAny(entry.Repository, ":@\\") || strings.ContainsFunc(entry.Branch, func(character rune) bool {
			return unicode.IsControl(character) || unicode.Is(unicode.Bidi_Control, character)
		}) {
			return nil, errPreview
		}
		reference, err := repository.ParseReference(entry.Repository, entry.Branch)
		if err != nil || reference.NameWithOwner != entry.Repository || reference.Branch != entry.Branch || seen[reference] {
			return nil, errPreview
		}
		seen[reference] = true
		candidates = append(candidates, Candidate{Index: index + 1, Repository: entry.Repository, Branch: entry.Branch, Existing: entry.ExistingRepositoryId != ""})
	}
	return candidates, nil
}

type Status string

const (
	Collected   Status = "collected"
	Failed      Status = "failed"
	Unknown     Status = "unknown"
	Unattempted Status = "unattempted"
)

type Outcome struct {
	Candidate Candidate
	Status    Status
	Code      connect.Code
}

type Result struct{ Outcomes []Outcome }

func (client *Client) ApplyFile(ctx context.Context, path, expectedSHA256 string, preview *Preview, selected []int) (Result, error) {
	release, err := client.acquire(ctx)
	if err != nil {
		return Result{}, err
	}
	defer release()
	result, err := client.selectCandidates(preview, selected)
	if err != nil {
		return Result{}, err
	}
	if !validHash(expectedSHA256) || expectedSHA256 != preview.sha256 {
		return result, errHash
	}
	data, digest, err := readExport(ctx, path)
	clear(data)
	if err != nil {
		return result, err
	}
	if digest != expectedSHA256 {
		return result, errHash
	}
	var incomplete bool
	for index := range result.Outcomes {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(ErrIncomplete, err)
		}
		outcome := &result.Outcomes[index]
		err := client.collect(ctx, outcome.Candidate)
		if err == nil {
			outcome.Status = Collected
			continue
		}
		incomplete = true
		outcome.Code = connect.CodeOf(err)
		if itemFailure(outcome.Code) && ctx.Err() == nil {
			outcome.Status = Failed
			continue
		}
		outcome.Status = Unknown
		return result, errors.Join(ErrIncomplete, err)
	}
	if incomplete {
		return result, ErrIncomplete
	}
	return result, nil
}

func (client *Client) selectCandidates(preview *Preview, selected []int) (Result, error) {
	if preview == nil || preview.client != client || len(selected) == 0 || len(selected) > len(preview.candidates) {
		return Result{}, errSelection
	}
	result := Result{Outcomes: make([]Outcome, 0, len(selected))}
	seen := make(map[int]bool, len(selected))
	for _, index := range selected {
		if index < 1 || index > len(preview.candidates) || seen[index] {
			return Result{}, errSelection
		}
		seen[index] = true
		result.Outcomes = append(result.Outcomes, Outcome{Candidate: preview.candidates[index-1], Status: Unattempted})
	}
	return result, nil
}

func (client *Client) collect(ctx context.Context, candidate Candidate) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	request := connect.NewRequest(&libraryv1.CollectRepositoryRequest{Repository: candidate.Repository, Branch: candidate.Branch})
	request.Header().Set("Authorization", "Bearer "+client.session)
	response, err := client.library.CollectRepository(ctx, request)
	if err != nil {
		return requestError(ctx, err)
	}
	entry := response.Msg.Repository
	if entry == nil || entry.NameWithOwner != candidate.Repository || entry.Branch != candidate.Branch || !validHash(entry.ArchiveHash) || entry.SkillCount == 0 || entry.SkillCount > 512 {
		return connect.NewError(connect.CodeUnknown, errors.New("invalid import collection response"))
	}
	return nil
}

func validHash(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func itemFailure(code connect.Code) bool {
	return code == connect.CodeInvalidArgument || code == connect.CodeNotFound || code == connect.CodeFailedPrecondition || code == connect.CodeDataLoss
}

func requestError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if errors.Is(err, context.Canceled) || connect.CodeOf(err) == connect.CodeCanceled {
		return connect.NewError(connect.CodeCanceled, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) || connect.CodeOf(err) == connect.CodeDeadlineExceeded {
		return connect.NewError(connect.CodeDeadlineExceeded, context.DeadlineExceeded)
	}
	return connect.NewError(connect.CodeOf(err), errors.New("import request failed"))
}
