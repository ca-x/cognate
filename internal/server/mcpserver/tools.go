package mcpserver

import (
	"context"
	"encoding/json/v2"
	"errors"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type PageInput struct {
	Offset int `json:"offset,omitzero" jsonschema:"Zero-based offset; at most 1000"`
	Limit  int `json:"limit,omitzero" jsonschema:"Page size; default 50, maximum 100"`
}

type IDInput struct {
	ID string `json:"id" jsonschema:"Owner-local opaque repository or Pack ID"`
}

type CollectInput struct {
	Repository string `json:"repository" jsonschema:"Public GitHub owner/repository, not an arbitrary URL"`
	Branch     string `json:"branch,omitempty" jsonschema:"Optional repository branch; no filesystem paths"`
}

type PackSelection struct {
	SkillID string `json:"skill_id"`
	Slug    string `json:"slug,omitempty"`
}

type PackDraft struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Items       []PackSelection `json:"items"`
}

type UpdatePackInput struct {
	ID               string    `json:"id"`
	ExpectedRevision int64     `json:"expected_revision"`
	Draft            PackDraft `json:"draft"`
}

type DeletePackInput struct {
	ID               string `json:"id"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type Skill struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	ContentHash string `json:"content_hash"`
}

type Repository struct {
	ID            string  `json:"id"`
	NameWithOwner string  `json:"name_with_owner"`
	Branch        string  `json:"branch"`
	SkillCount    int     `json:"skill_count"`
	Skills        []Skill `json:"skills"`
}

type CatalogPage struct {
	Repositories []Repository `json:"repositories"`
	NextOffset   int          `json:"next_offset"`
}

type PackItem struct {
	SkillID      string `json:"skill_id"`
	Slug         string `json:"slug"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	ContentHash  string `json:"content_hash"`
	RepositoryID string `json:"repository_id"`
}

type Pack struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Revision    int64      `json:"revision"`
	ItemCount   int        `json:"item_count"`
	Items       []PackItem `json:"items"`
}

type PackPage struct {
	Packs      []Pack `json:"packs"`
	NextOffset int    `json:"next_offset"`
}

type Deleted struct {
	Deleted bool `json:"deleted"`
}

func (server *Server) addTools(sdk *mcp.Server) {
	server.addTool(sdk, "catalog_list", state.MCPCatalogRead, "List your collected repositories. Requires catalog:read.", func(ctx context.Context, owner string, input PageInput) (CatalogPage, error) {
		start, limit, err := pageBounds(input)
		if err != nil {
			return CatalogPage{}, err
		}
		entries, err := server.library.List(ctx, owner)
		if err != nil {
			return CatalogPage{}, err
		}
		start, end, next := pageRange(start, limit, len(entries))
		result := CatalogPage{Repositories: make([]Repository, 0, end-start), NextOffset: next}
		for _, entry := range entries[start:end] {
			result.Repositories = append(result.Repositories, repositoryOutput(entry))
		}
		return result, nil
	})
	server.addTool(sdk, "catalog_get", state.MCPCatalogRead, "Get one of your collected repositories and skill IDs. Requires catalog:read.", func(ctx context.Context, owner string, input IDInput) (Repository, error) {
		entry, err := server.library.Get(ctx, owner, input.ID)
		return repositoryOutput(entry), err
	})
	server.addTool(sdk, "catalog_collect", state.MCPCatalogWrite, "Collect a public GitHub repository into your library; does not publish it. Requires catalog:write.", func(ctx context.Context, owner string, input CollectInput) (Repository, error) {
		reference, err := repository.ParseReference(input.Repository, input.Branch)
		if err != nil || input.Repository != reference.NameWithOwner {
			return Repository{}, state.ErrInvalidCollection
		}
		entry, err := server.library.Collect(ctx, owner, reference.NameWithOwner, reference.Branch)
		return repositoryOutput(entry), err
	})
	server.addTool(sdk, "packs_list", state.MCPPacksRead, "List your Packs. Requires packs:read.", func(ctx context.Context, owner string, input PageInput) (PackPage, error) {
		start, limit, err := pageBounds(input)
		if err != nil {
			return PackPage{}, err
		}
		entries, err := server.store.ListPacks(ctx, owner)
		if err != nil {
			return PackPage{}, err
		}
		start, end, next := pageRange(start, limit, len(entries))
		result := PackPage{Packs: make([]Pack, 0, end-start), NextOffset: next}
		for _, entry := range entries[start:end] {
			result.Packs = append(result.Packs, packOutput(entry))
		}
		return result, nil
	})
	server.addTool(sdk, "packs_get", state.MCPPacksRead, "Get one of your Packs. Requires packs:read.", func(ctx context.Context, owner string, input IDInput) (Pack, error) {
		entry, err := server.store.Pack(ctx, owner, input.ID)
		return packOutput(entry), err
	})
	server.addTool(sdk, "packs_create", state.MCPPacksWrite, "Create a Pack from your collected skill IDs. Does not create a share or publish. Requires packs:write.", func(ctx context.Context, owner string, input PackDraft) (Pack, error) {
		entry, err := server.store.CreatePack(ctx, owner, packDraft(input))
		return packOutput(entry), err
	})
	server.addTool(sdk, "packs_update", state.MCPPacksWrite, "Replace your Pack using expected_revision CAS. Requires packs:write.", func(ctx context.Context, owner string, input UpdatePackInput) (Pack, error) {
		entry, err := server.store.UpdatePack(ctx, owner, input.ID, input.ExpectedRevision, packDraft(input.Draft))
		return packOutput(entry), err
	})
	server.addTool(sdk, "packs_delete", state.MCPPacksWrite, "Delete your Pack using expected_revision CAS. Requires packs:write.", func(ctx context.Context, owner string, input DeletePackInput) (Deleted, error) {
		err := server.store.DeletePack(ctx, owner, input.ID, input.ExpectedRevision)
		return Deleted{Deleted: err == nil}, err
	})
}

func (server *Server) addTool[Input, Output any](sdk *mcp.Server, name, scope, description string, handler func(context.Context, string, Input) (Output, error)) {
	mcp.AddTool(sdk, &mcp.Tool{Name: name, Description: description}, func(ctx context.Context, request *mcp.CallToolRequest, input Input) (*mcp.CallToolResult, Output, error) {
		var zero Output
		httpContext, ok := ctx.Value(requestContextKey{}).(context.Context)
		if !ok {
			return nil, zero, state.ErrMCPUnauthorized
		}
		if err := httpContext.Err(); err != nil {
			return nil, zero, safeError(err)
		}
		deadline, ok := httpContext.Deadline()
		if !ok {
			return nil, zero, state.ErrMCPUnauthorized
		}
		ctx, cancel := context.WithDeadline(ctx, deadline)
		stop := context.AfterFunc(httpContext, cancel)
		defer func() { stop(); cancel() }()
		ctx, done, err := server.begin(ctx, server.toolSlots)
		if err != nil {
			return nil, zero, safeError(err)
		}
		defer done()
		if request.Extra == nil {
			return nil, zero, state.ErrMCPUnauthorized
		}
		grant, err := server.access.Authorize(ctx, bearer(request.Extra.Header), scope)
		if err != nil {
			if errors.Is(err, state.ErrMCPForbidden) {
				if auditErr := server.access.Audit(ctx, grant.CredentialID, name, resourceID(input), "denied"); auditErr != nil {
					return nil, zero, state.ErrMCPUnavailable
				}
			}
			return nil, zero, safeError(err)
		}
		output, operationErr := handler(ctx, grant.Owner, input)
		if operationErr == nil {
			data, err := json.Marshal(output)
			if err != nil || len(data) > MaxResultBytes/4 {
				operationErr = ErrTooLarge
			}
		}
		outcome := "success"
		if operationErr != nil {
			outcome = "failure"
		}
		resource := resourceID(input)
		if resource == "" && operationErr == nil {
			resource = resourceID(output)
		}
		if err := server.access.Audit(ctx, grant.CredentialID, name, resource, outcome); err != nil {
			return nil, zero, state.ErrMCPUnavailable
		}
		if operationErr != nil {
			return nil, zero, safeError(operationErr)
		}
		return nil, output, nil
	})
}

func resourceID(input any) string {
	var id string
	switch value := input.(type) {
	case IDInput:
		id = value.ID
	case UpdatePackInput:
		id = value.ID
	case DeletePackInput:
		id = value.ID
	case Repository:
		id = value.ID
	case Pack:
		id = value.ID
	}
	if len(id) != 26 {
		return ""
	}
	for _, letter := range id {
		if !(letter >= 'A' && letter <= 'Z' || letter >= '2' && letter <= '7') {
			return ""
		}
	}
	return id
}

func safeError(err error) error {
	for _, known := range []error{state.ErrMCPUnauthorized, state.ErrMCPForbidden, state.ErrMCPUnavailable, state.ErrPackNotFound, state.ErrPackConflict, state.ErrPackInvalid, state.ErrPackSlugConflict, state.ErrPackSkillUnavailable, state.ErrPackLimit, state.ErrRepositoryNotFound, state.ErrLibraryLimit, state.ErrInvalidCollection, library.ErrCapacity, library.ErrClosed, ErrBusy, ErrClosed, ErrTooLarge} {
		if errors.Is(err, known) {
			return known
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return errors.New("MCP operation canceled")
	}
	return errors.New("MCP operation unavailable")
}

func pageBounds(input PageInput) (int, int, error) {
	if input.Limit == 0 {
		input.Limit = 50
	}
	if input.Offset < 0 || input.Offset > 1000 || input.Limit < 1 || input.Limit > 100 {
		return 0, 0, errors.New("invalid page bounds")
	}
	return input.Offset, input.Limit, nil
}

func pageRange(start, limit, size int) (int, int, int) {
	start = min(start, size)
	end := min(start+limit, size)
	next := 0
	if end < size {
		next = end
	}
	return start, end, next
}

func repositoryOutput(entry state.CollectedRepository) Repository {
	result := Repository{ID: entry.ID, NameWithOwner: entry.NameWithOwner, Branch: entry.Branch, SkillCount: entry.SkillCount, Skills: make([]Skill, 0, len(entry.Skills))}
	for _, item := range entry.Skills {
		result.Skills = append(result.Skills, Skill{ID: item.ID, Name: item.Name, Description: item.Description, ContentHash: item.ContentHash})
	}
	return result
}

func packOutput(entry state.Pack) Pack {
	result := Pack{ID: entry.ID, Name: entry.Name, Description: entry.Description, Revision: entry.Revision, ItemCount: entry.ItemCount, Items: make([]PackItem, 0, len(entry.Items))}
	for _, item := range entry.Items {
		result.Items = append(result.Items, PackItem{SkillID: item.SkillID, Slug: item.Slug, Name: item.Name, Description: item.Description, ContentHash: item.ContentHash, RepositoryID: item.RepositoryID})
	}
	return result
}

func packDraft(input PackDraft) state.PackDraft {
	result := state.PackDraft{Name: input.Name, Description: input.Description, Items: make([]state.PackSelection, 0, len(input.Items))}
	for _, item := range input.Items {
		result.Items = append(result.Items, state.PackSelection{SkillID: item.SkillID, Slug: item.Slug})
	}
	return result
}
