package mcpserver_test

import (
	"encoding/json/v2"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/server/mcpserver"
	"github.com/ca-x/cognate/internal/server/state"
)

type categoryResult struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Revision   int64  `json:"revision"`
	SkillCount int    `json:"skill_count"`
}

type categoriesResult struct {
	Categories []categoryResult `json:"categories"`
}
type publicationResult struct {
	RepositoryID string `json:"repository_id"`
	Published    bool   `json:"published"`
	Revision     int64  `json:"revision"`
}

func TestSDKClassificationAndPublication(test *testing.T) {
	fixture := newFixture(test, nil)
	_, endpoint := fixture.endpoint(test, mcpserver.Options{})
	repository, err := fixture.library.Collect(test.Context(), "alice", "alice/example", "main")
	if err != nil {
		test.Fatal(err)
	}
	repository, err = fixture.library.Get(test.Context(), "alice", repository.ID)
	if err != nil {
		test.Fatal(err)
	}
	skillID := repository.Skills[0].ID
	credential, secret := fixture.token(test, "alice", "categories:read", "categories:write", "publication:write", state.MCPCatalogRead)
	client, _ := sdkClient(test, endpoint, secret)
	created := decode[categoryResult](test, call(test, client, "categories_create", map[string]any{"name": "Reference"}))
	if created.ID == "" || created.Revision != 1 {
		test.Fatalf("missing category: %+v", created)
	}
	page := decode[mcpserver.CategoryPage](test, call(test, client, "categories_list", mcpserver.PageInput{Limit: 1}))
	if len(page.Categories) != 1 || page.Categories[0].ID != created.ID || page.NextOffset != 0 {
		test.Fatalf("category listing missing: %+v", page)
	}
	assigned := decode[categoriesResult](test, call(test, client, "categories_assign", map[string]any{"skill_id": skillID, "category_ids": []string{created.ID}}))
	if len(assigned.Categories) != 1 || assigned.Categories[0].SkillCount != 1 {
		test.Fatalf("association missing: %+v", assigned)
	}
	got := decode[categoriesResult](test, call(test, client, "categories_get", map[string]any{"skill_id": skillID}))
	if len(got.Categories) != 1 {
		test.Fatal("get lost association")
	}
	updated := decode[categoryResult](test, call(test, client, "categories_update", map[string]any{"id": created.ID, "expected_revision": 1, "name": "Renamed"}))
	if updated.Revision != 2 || updated.Name != "Renamed" {
		test.Fatalf("update failed: %+v", updated)
	}
	if !call(test, client, "categories_delete", map[string]any{"id": created.ID, "expected_revision": 1}).IsError {
		test.Fatal("stale category delete succeeded")
	}
	publication := decode[publicationResult](test, call(test, client, "publication_get", map[string]any{"id": repository.ID}))
	if publication.Published || publication.Revision != 1 {
		test.Fatalf("not private by default: %+v", publication)
	}
	if !call(test, client, "publication_publish", map[string]any{"id": repository.ID, "expected_revision": 1}).IsError {
		test.Fatal("publication without explicit confirmation succeeded")
	}
	if !call(test, client, "publication_publish", map[string]any{"id": repository.ID, "expected_revision": 1, "confirm_full_contents": false}).IsError {
		test.Fatal("publication with false confirmation succeeded")
	}
	published := decode[publicationResult](test, call(test, client, "publication_publish", map[string]any{"id": repository.ID, "expected_revision": 1, "confirm_full_contents": true}))
	if !published.Published || published.Revision != 2 {
		test.Fatalf("publish failed: %+v", published)
	}
	if !call(test, client, "publication_unpublish", map[string]any{"id": repository.ID, "expected_revision": 1}).IsError {
		test.Fatal("stale publication CAS succeeded")
	}
	unpublished := decode[publicationResult](test, call(test, client, "publication_unpublish", map[string]any{"id": repository.ID, "expected_revision": 2}))
	if unpublished.Published || unpublished.Revision != 3 {
		test.Fatal("unpublish failed")
	}
	decode[mcpserver.Deleted](test, call(test, client, "categories_delete", map[string]any{"id": created.ID, "expected_revision": 2}))
	got = decode[categoriesResult](test, call(test, client, "categories_get", map[string]any{"skill_id": skillID}))
	if len(got.Categories) != 0 {
		test.Fatal("delete retained category association")
	}
	after, err := fixture.library.Get(test.Context(), "alice", repository.ID)
	if err != nil || len(after.Skills) != len(repository.Skills) || after.Skills[0].ContentHash != repository.Skills[0].ContentHash || after.Skills[0].ID != skillID {
		test.Fatalf("classification changed skill/hash: %v", err)
	}
	audits, err := fixture.access.ListAudit(test.Context(), "alice", credential.ID)
	if err != nil {
		test.Fatal(err)
	}
	data, err := json.Marshal(audits)
	if err != nil || strings.Contains(string(data), "Reference") || strings.Contains(string(data), "Renamed") || strings.Contains(string(data), secret.Reveal()) {
		test.Fatal("audit contains input text or credential")
	}
	var createdAudited bool
	for _, audit := range audits {
		if audit.Action == "categories_create" && audit.Outcome == "success" && audit.Resource == created.ID {
			createdAudited = true
		}
	}
	if !createdAudited {
		test.Fatal("category creation lacks resource-only audit")
	}
}

func TestSDKClassificationScopeIsolation(test *testing.T) {
	fixture := newFixture(test, nil)
	_, endpoint := fixture.endpoint(test, mcpserver.Options{})
	repository, err := fixture.library.Collect(test.Context(), "alice", "alice/example", "main")
	if err != nil {
		test.Fatal(err)
	}
	repository, err = fixture.library.Get(test.Context(), "alice", repository.ID)
	if err != nil {
		test.Fatal(err)
	}
	category, err := fixture.store.CreateCategory(test.Context(), "alice", "Original")
	if err != nil {
		test.Fatal(err)
	}
	tools := []struct {
		name, scope string
		args        any
	}{
		{"categories_list", "categories:read", mcpserver.PageInput{}},
		{"categories_get", "categories:read", map[string]any{"skill_id": repository.Skills[0].ID}},
		{"categories_create", "categories:write", map[string]any{"name": "New"}},
		{"categories_update", "categories:write", map[string]any{"id": category.ID, "expected_revision": 1, "name": "New"}},
		{"categories_delete", "categories:write", map[string]any{"id": category.ID, "expected_revision": 1}},
		{"categories_assign", "categories:write", map[string]any{"skill_id": repository.Skills[0].ID, "category_ids": []string{category.ID}}},
		{"publication_get", state.MCPCatalogRead, map[string]any{"id": repository.ID}},
		{"publication_publish", "publication:write", map[string]any{"id": repository.ID, "expected_revision": 1, "confirm_full_contents": true}},
		{"publication_unpublish", "publication:write", map[string]any{"id": repository.ID, "expected_revision": 1}},
	}
	for _, scope := range []string{state.MCPCatalogRead, state.MCPCatalogWrite, state.MCPPacksRead, state.MCPPacksWrite, "categories:read", "categories:write", "publication:write"} {
		test.Run(scope, func(test *testing.T) {
			_, secret := fixture.token(test, "alice", scope)
			client, _ := sdkClient(test, endpoint, secret)
			for _, tool := range tools {
				if tool.scope != scope && !call(test, client, tool.name, tool.args).IsError {
					test.Fatalf("%s granted %s", scope, tool.name)
				}
			}
		})
	}
	_, old := fixture.token(test, "alice", state.MCPCatalogRead, state.MCPCatalogWrite, state.MCPPacksRead, state.MCPPacksWrite)
	oldClient, _ := sdkClient(test, endpoint, old)
	for _, tool := range tools {
		if tool.scope != state.MCPCatalogRead && !call(test, oldClient, tool.name, tool.args).IsError {
			test.Fatalf("old four scopes granted %s", tool.name)
		}
	}
	_, foreign := fixture.token(test, "bob", "categories:read", "categories:write", "publication:write", state.MCPCatalogRead)
	foreignClient, _ := sdkClient(test, endpoint, foreign)
	for _, tool := range tools {
		if tool.name != "categories_list" && tool.name != "categories_create" && !call(test, foreignClient, tool.name, tool.args).IsError {
			test.Fatalf("foreign %s succeeded", tool.name)
		}
	}
	listed := decode[categoriesResult](test, call(test, foreignClient, "categories_list", mcpserver.PageInput{}))
	if len(listed.Categories) != 0 {
		test.Fatal("foreign category leaked")
	}
	foreignRepository, err := fixture.library.Collect(test.Context(), "bob", "bob/example", "main")
	if err != nil {
		test.Fatal(err)
	}
	foreignRepository, err = fixture.library.Get(test.Context(), "bob", foreignRepository.ID)
	if err != nil {
		test.Fatal(err)
	}
	if !call(test, foreignClient, "categories_assign", map[string]any{"skill_id": foreignRepository.Skills[0].ID, "category_ids": []string{category.ID}}).IsError {
		test.Fatal("foreign category attached to own skill")
	}
	stateAfter, err := fixture.store.Publication(test.Context(), "alice", repository.ID)
	if err != nil || stateAfter.Published || stateAfter.Revision != 1 {
		test.Fatalf("denied call changed publication: %v", err)
	}
}
