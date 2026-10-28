package packclient

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	catalogv1 "github.com/ca-x/skillhub/gen/skillhub/catalog/v1"
	"github.com/ca-x/skillhub/gen/skillhub/catalog/v1/catalogv1connect"
	"github.com/ca-x/skillhub/internal/skill"
)

type PublicClient struct {
	lookup   catalogv1connect.PublicCatalogServiceClient
	download catalogv1connect.PublicCatalogServiceClient
	slots    chan struct{}
}

func NewPublic(serverURL string, client *http.Client) (*PublicClient, error) {
	origin, err := parseOrigin(serverURL)
	if err != nil {
		return nil, err
	}
	bounded := newBoundedHTTPClient(origin, client)
	bounded.Transport = anonymousTransport{base: bounded.Transport}
	return &PublicClient{
		lookup:   catalogv1connect.NewPublicCatalogServiceClient(bounded, origin.String(), connect.WithReadMaxBytes(64<<10)),
		download: catalogv1connect.NewPublicCatalogServiceClient(bounded, origin.String(), connect.WithReadMaxBytes(maxChunkBytes+1024)),
		slots:    make(chan struct{}, 2),
	}, nil
}

type anonymousTransport struct{ base http.RoundTripper }

func (transport anonymousTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Del("Authorization")
	request.Header.Del("Proxy-Authorization")
	request.Header.Del("Cookie")
	return transport.base.RoundTrip(request)
}

func (client *PublicClient) acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if client == nil || client.slots == nil {
		return nil, errors.New("public skill client is not initialized")
	}
	select {
	case client.slots <- struct{}{}:
		return func() { <-client.slots }, nil
	default:
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("public skill client is busy"))
	}
}

func (client *PublicClient) GetSkill(ctx context.Context, id string) (*catalogv1.PublicSkill, error) {
	if !validID(id) {
		return nil, errManifest
	}
	release, err := client.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	response, err := client.lookup.GetSkill(ctx, connect.NewRequest(&catalogv1.GetSkillRequest{SkillId: id}))
	if err != nil {
		return nil, requestError(ctx, err)
	}
	if response.Msg.Skill == nil || response.Msg.Skill.Id != id || validatePublicSkill(response.Msg.Skill) != nil {
		return nil, errManifest
	}
	return response.Msg.Skill, nil
}

func (client *PublicClient) Download(ctx context.Context, selected *catalogv1.PublicSkill) (*Snapshot, error) {
	if err := validatePublicSkill(selected); err != nil {
		return nil, err
	}
	release, err := client.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	return downloadSnapshot(ctx, selected.ContentHash, func(destination io.Writer) error {
		stream, err := client.download.DownloadSkill(ctx, connect.NewRequest(&catalogv1.DownloadSkillRequest{SkillId: selected.Id, ContentHash: selected.ContentHash, PublicationRevision: selected.PublicationRevision}))
		if err != nil {
			return requestError(ctx, err)
		}
		return receiveSkill(ctx, destination, stream, selected.ContentHash)
	})
}

func validatePublicSkill(entry *catalogv1.PublicSkill) error {
	if entry == nil || !validID(entry.Id) || !validID(entry.RepositoryId) || skill.ValidatePortableSlug(entry.Slug) != nil || !validHash(entry.ContentHash) || entry.PublicationRevision < 1 || !validText(entry.Name, 256, false) || strings.TrimSpace(entry.Name) == "" || !validText(entry.Description, 4096, true) || !validText(entry.RepositoryName, 256, false) || strings.TrimSpace(entry.RepositoryName) == "" || !validText(entry.Branch, 1024, false) || !validText(entry.Path, 1024, false) || !fs.ValidPath(entry.Path) || strings.Contains(entry.Path, "\\") || len(entry.Categories) > 8 {
		return errManifest
	}
	seen := make(map[string]bool, len(entry.Categories))
	for _, category := range entry.Categories {
		if category == nil || !validID(category.Id) || !validText(category.Name, 80, false) || strings.TrimSpace(category.Name) == "" || category.SkillCount == 0 || seen[category.Id] {
			return errManifest
		}
		seen[category.Id] = true
	}
	return nil
}
