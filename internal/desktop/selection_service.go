package desktop

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"connectrpc.com/connect"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	libraryv1 "github.com/ca-x/cognate/gen/skillhub/library/v1"
	"github.com/ca-x/cognate/gen/skillhub/library/v1/libraryv1connect"
	"github.com/ca-x/cognate/internal/importclient"
	"github.com/ca-x/cognate/internal/repository"
	"google.golang.org/protobuf/proto"
)

var errSelectionBlocked = errors.New("此身份有结果不明的导入，只能查询原操作状态，不得重发")
var errSelectionInvalid = errors.New("导入文件、选项或服务响应无效；请核对并重新预览")

type selectionBackend interface {
	OpenSelection(context.Context, string, string) (*selectionConnection, error)
}

type selectionConnection struct {
	selectionPrivate
	Origin, Owner string
	private       func() *selectionConnectionData
}

type selectionConnectionData struct {
	mutex    sync.Mutex
	once     sync.Once
	workers  sync.WaitGroup
	closed   bool
	life     context.Context
	cancel   context.CancelFunc
	identity configurationIdentity
	client   *importclient.SelectionClient
	service  *Service
}

func (service *Service) OpenSelection(ctx context.Context, origin, session string) (*selectionConnection, error) {
	identity, err := service.ConfigurationIdentity(ctx, origin, session)
	if err != nil {
		return nil, err
	}
	client, err := importclient.NewSelectionClient(identity.Origin, session, service.client(identity.Origin))
	if err != nil {
		identity.clear()
		return nil, errSelectionInvalid
	}
	if ctx.Err() != nil {
		client.Close()
		identity.clear()
		return nil, ctx.Err()
	}
	life, cancel := context.WithCancel(context.Background())
	data := &selectionConnectionData{identity: identity, client: client, service: service, life: life, cancel: cancel}
	return &selectionConnection{Origin: identity.Origin, Owner: identity.OwnerID, private: func() *selectionConnectionData { return data }}, nil
}

func (connection *selectionConnection) start(caller context.Context) (context.Context, func(), error) {
	if connection == nil || connection.private == nil {
		return nil, nil, errSelectionInvalid
	}
	data := connection.private()
	if connection.Origin != data.identity.Origin || connection.Owner != data.identity.OwnerID {
		return nil, nil, errConfigurationIdentity
	}
	data.mutex.Lock()
	if data.closed || caller.Err() != nil {
		data.mutex.Unlock()
		return nil, nil, errSelectionInvalid
	}
	ctx, cancel := context.WithCancel(caller)
	stop := context.AfterFunc(data.life, cancel)
	data.workers.Add(1)
	data.mutex.Unlock()
	done := func() { stop(); cancel(); data.workers.Done() }
	identityData := data.identity.private()
	identityData.mutex.Lock()
	session := identityData.session
	identityData.mutex.Unlock()
	fresh, err := data.service.ConfigurationIdentity(ctx, connection.Origin, session)
	if err != nil {
		done()
		return nil, nil, err
	}
	defer fresh.clear()
	if fresh.OwnerID != connection.Owner {
		done()
		return nil, nil, errConfigurationIdentity
	}
	return ctx, done, nil
}

func (connection *selectionConnection) stop() {
	data := connection.private()
	data.mutex.Lock()
	data.closed = true
	data.cancel()
	data.mutex.Unlock()
}

func (connection *selectionConnection) Close() {
	if connection == nil || connection.private == nil {
		return
	}
	data := connection.private()
	data.once.Do(func() {
		connection.stop()
		data.workers.Wait()
		data.client.Close()
		data.identity.clear()
	})
}

func (connection *selectionConnection) Preview(ctx context.Context, source importv1.ImportSelectionSource, file, hash string, options []*importv1.ImportCandidateOptions) (*importclient.SelectionPreview, error) {
	ctx, done, err := connection.start(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	if err := selectionFileAdmission(file); err != nil {
		return nil, err
	}
	preview, err := connection.private().client.PreviewFile(ctx, source, file, hash, options)
	if err != nil {
		return nil, selectionFailure(err)
	}
	return preview, nil
}

func (connection *selectionConnection) Begin(preview *importclient.SelectionPreview, choice importclient.SelectionChoice) (*importclient.SelectionOperation, error) {
	data := connection.private()
	data.mutex.Lock()
	defer data.mutex.Unlock()
	if data.closed {
		return nil, errSelectionInvalid
	}
	operation, err := data.client.BeginSelection(preview, choice)
	if err != nil {
		return nil, selectionFailure(err)
	}
	return operation, nil
}

func (connection *selectionConnection) Commit(ctx context.Context, operation *importclient.SelectionOperation, file string) (importclient.SelectionResult, error) {
	defer operation.Close()
	ctx, done, err := connection.start(ctx)
	if err != nil {
		return importclient.SelectionResult{OperationID: operation.ID()}, err
	}
	defer done()
	if err := selectionFileAdmission(file); err != nil {
		return importclient.SelectionResult{OperationID: operation.ID()}, err
	}
	result, err := operation.CommitFile(ctx, file)
	return result, selectionFailure(err)
}

func selectionFileAdmission(file string) error {
	if !filepath.IsAbs(file) || filepath.Clean(file) != file {
		return errSelectionInvalid
	}
	info, err := os.Lstat(file)
	if err != nil || !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return errSelectionInvalid
	}
	return nil
}

func (connection *selectionConnection) Status(ctx context.Context, id string) (importclient.SelectionResult, error) {
	ctx, done, err := connection.start(ctx)
	if err != nil {
		return importclient.SelectionResult{}, err
	}
	defer done()
	result, err := connection.private().client.Status(ctx, id)
	return result, selectionFailure(err)
}

func selectionFailure(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{importclient.ErrSelectionUnknown, importclient.ErrIncomplete, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, known) {
			return known
		}
	}
	return errSelectionInvalid
}

func selectionHash(value string) bool {
	return len(value) == 64 && !strings.ContainsFunc(value, func(letter rune) bool { return !(letter >= 'a' && letter <= 'f' || letter >= '0' && letter <= '9') })
}

func (connection *selectionConnection) Bindings(ctx context.Context, name, branch string) ([]*importv1.SkillBinding, error) {
	reference, err := repository.ParseReference(name, branch)
	if err != nil || reference.NameWithOwner != name || reference.Branch != branch {
		return nil, errSelectionInvalid
	}
	ctx, done, err := connection.start(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	data := connection.private()
	client := libraryv1connect.NewLibraryServiceClient(data.service.client(connection.Origin), connection.Origin, connect.WithReadMaxBytes(2<<20), connect.WithSendMaxBytes(64<<10))
	list := connect.NewRequest(&libraryv1.ListRepositoriesRequest{})
	if err := data.identity.authorize(list.Header()); err != nil {
		return nil, err
	}
	response, err := client.ListRepositories(ctx, list)
	if err != nil {
		return nil, networkError(err)
	}
	if !configurationKnown(response.Msg) {
		return nil, errSelectionInvalid
	}
	var bindings []*importv1.SkillBinding
	seen := make(map[string]bool)
	for _, candidate := range response.Msg.Repositories {
		if candidate == nil || !configurationText(candidate.Id, 128) || seen[candidate.Id] {
			return nil, errSelectionInvalid
		}
		seen[candidate.Id] = true
		if candidate.NameWithOwner != name || candidate.Branch != branch {
			continue
		}
		if !selectionHash(candidate.ArchiveHash) {
			return nil, errSelectionInvalid
		}
		request := connect.NewRequest(&libraryv1.GetRepositoryRequest{RepositoryId: candidate.Id})
		if err := data.identity.authorize(request.Header()); err != nil {
			return nil, err
		}
		detail, err := client.GetRepository(ctx, request)
		if err != nil {
			return nil, networkError(err)
		}
		if !configurationKnown(detail.Msg) || !proto.Equal(candidate, detail.Msg.Repository) || uint64(len(detail.Msg.Skills)) != uint64(candidate.SkillCount) {
			return nil, errSelectionInvalid
		}
		skills := make(map[string]bool)
		for _, skill := range detail.Msg.Skills {
			if skill == nil || !configurationText(skill.Id, 128) || skills[skill.Id] || !selectionHash(skill.ContentHash) || !configurationText(skill.Path, 4096) || path.Clean(skill.Path) != skill.Path || strings.HasPrefix(skill.Path, "/") || strings.Contains(skill.Path, "\\") || skill.Path == ".." || strings.HasPrefix(skill.Path, "../") {
				return nil, errSelectionInvalid
			}
			skills[skill.Id] = true
			bindings = append(bindings, &importv1.SkillBinding{RepositoryId: candidate.Id, ArchiveHash: candidate.ArchiveHash, SkillId: skill.Id, LogicalPath: skill.Path, ContentHash: skill.ContentHash, Repository: name, Branch: branch})
		}
	}
	return bindings, nil
}
