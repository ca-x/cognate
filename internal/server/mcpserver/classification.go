package mcpserver

import (
	"context"

	"github.com/ca-x/cognate/internal/server/state"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type CreateCategoryInput struct {
	Name string `json:"name" jsonschema:"Category name, at most 80 UTF-8 bytes"`
}

type UpdateCategoryInput struct {
	ID               string `json:"id"`
	ExpectedRevision int64  `json:"expected_revision"`
	Name             string `json:"name"`
}

type DeleteCategoryInput struct {
	ID               string `json:"id"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type SkillCategoriesInput struct {
	SkillID string `json:"skill_id" jsonschema:"Owner-local opaque skill ID"`
}

type AssignCategoriesInput struct {
	SkillID     string   `json:"skill_id"`
	CategoryIDs []string `json:"category_ids" jsonschema:"Replace all categories for this skill; at most 8 unique owner-local IDs; empty array clears associations"`
}

type PublishInput struct {
	ID                  string `json:"id" jsonschema:"Owner-local opaque repository ID"`
	ExpectedRevision    int64  `json:"expected_revision"`
	ConfirmFullContents bool   `json:"confirm_full_contents" jsonschema:"Must explicitly be true: publishes the full contents of every skill directory, not just SKILL.md"`
}

type UnpublishInput struct {
	ID               string `json:"id" jsonschema:"Owner-local opaque repository ID"`
	ExpectedRevision int64  `json:"expected_revision"`
}

type Category struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	SkillCount int    `json:"skill_count"`
	Revision   int64  `json:"revision"`
}

type Categories struct {
	Categories []Category `json:"categories"`
}

type CategoryPage struct {
	Categories []Category `json:"categories"`
	NextOffset int        `json:"next_offset"`
}

type Publication struct {
	RepositoryID string `json:"repository_id"`
	Published    bool   `json:"published"`
	Revision     int64  `json:"revision"`
}

func (server *Server) addClassificationTools(sdk *mcp.Server) {
	server.addTool(sdk, "categories_list", state.MCPCategoriesRead, "List your categories and skill counts. Requires categories:read.", func(ctx context.Context, owner string, input PageInput) (CategoryPage, error) {
		start, limit, err := pageBounds(input)
		if err != nil {
			return CategoryPage{}, err
		}
		entries, err := server.store.ListCategories(ctx, owner)
		if err != nil {
			return CategoryPage{}, err
		}
		start, end, next := pageRange(start, limit, len(entries))
		return CategoryPage{Categories: categoriesOutput(entries[start:end]), NextOffset: next}, nil
	})
	server.addTool(sdk, "categories_get", state.MCPCategoriesRead, "Get categories assigned to one of your skills. Requires categories:read.", func(ctx context.Context, owner string, input SkillCategoriesInput) (Categories, error) {
		entries, err := server.store.SkillCategories(ctx, owner, input.SkillID)
		return Categories{Categories: categoriesOutput(entries)}, err
	})
	server.addTool(sdk, "categories_create", state.MCPCategoriesWrite, "Create a category in your namespace; does not publish anything. Requires categories:write.", func(ctx context.Context, owner string, input CreateCategoryInput) (Category, error) {
		entry, err := server.store.CreateCategory(ctx, owner, input.Name)
		return categoryOutput(entry), err
	})
	server.addTool(sdk, "categories_update", state.MCPCategoriesWrite, "Rename your category using expected_revision CAS. Requires categories:write.", func(ctx context.Context, owner string, input UpdateCategoryInput) (Category, error) {
		entry, err := server.store.UpdateCategory(ctx, owner, input.ID, input.ExpectedRevision, input.Name)
		return categoryOutput(entry), err
	})
	server.addTool(sdk, "categories_delete", state.MCPCategoriesWrite, "Delete your category using expected_revision CAS, removing associations without deleting skills or changing content hashes. Requires categories:write.", func(ctx context.Context, owner string, input DeleteCategoryInput) (Deleted, error) {
		err := server.store.DeleteCategory(ctx, owner, input.ID, input.ExpectedRevision)
		return Deleted{Deleted: err == nil}, err
	})
	server.addTool(sdk, "categories_assign", state.MCPCategoriesWrite, "Replace a skill's categories with up to 8 of your category IDs; empty array clears them. Does not change skill contents, hashes, or publication. Requires categories:write.", func(ctx context.Context, owner string, input AssignCategoriesInput) (Categories, error) {
		entries, err := server.store.SetSkillCategories(ctx, owner, input.SkillID, input.CategoryIDs)
		return Categories{Categories: categoriesOutput(entries)}, err
	})
	server.addTool(sdk, "publication_get", state.MCPCatalogRead, "Read publication status and revision of your repository. Requires catalog:read, not publication:write.", func(ctx context.Context, owner string, input IDInput) (Publication, error) {
		entry, err := server.store.Publication(ctx, owner, input.ID)
		return publicationOutput(entry), err
	})
	server.addTool(sdk, "publication_publish", state.MCPPublicationWrite, "Publish your repository using expected_revision CAS. Explicit confirm_full_contents=true is mandatory: ALL skill directory contents become public. Requires publication:write; catalog:write and categories:write do not authorize publishing.", func(ctx context.Context, owner string, input PublishInput) (Publication, error) {
		entry, err := server.store.SetPublication(ctx, owner, input.ID, input.ExpectedRevision, true, input.ConfirmFullContents)
		return publicationOutput(entry), err
	})
	server.addTool(sdk, "publication_unpublish", state.MCPPublicationWrite, "Unpublish your repository using expected_revision CAS. Requires publication:write.", func(ctx context.Context, owner string, input UnpublishInput) (Publication, error) {
		entry, err := server.store.SetPublication(ctx, owner, input.ID, input.ExpectedRevision, false, false)
		return publicationOutput(entry), err
	})
}

func categoryOutput(entry state.Category) Category {
	return Category{ID: entry.ID, Name: entry.Name, SkillCount: entry.SkillCount, Revision: entry.Revision}
}

func categoriesOutput(entries []state.Category) []Category {
	result := make([]Category, 0, len(entries))
	for _, entry := range entries {
		result = append(result, categoryOutput(entry))
	}
	return result
}

func publicationOutput(entry state.Publication) Publication {
	return Publication{RepositoryID: entry.RepositoryID, Published: entry.Published, Revision: entry.Revision}
}
