package httpapi

import (
	"crypto/rand"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	catalogv1 "github.com/ca-x/cognate/gen/skillhub/catalog/v1"
	"github.com/ca-x/cognate/gen/skillhub/catalog/v1/catalogv1connect"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
)

func TestCatalogOwnerRPCIsolationAndCAS(test *testing.T) {
	authentication, store := testServiceAndStore(test)
	sessions := make(map[string]string)
	owners := make(map[string]string)
	collected := make(map[string]state.CollectedRepository)
	for _, name := range []string{"alice", "bob"} {
		password := []byte(rand.Text())
		owner, err := authentication.Register(test.Context(), name, password)
		if err != nil {
			test.Fatal(err)
		}
		owners[name] = owner
		session, err := authentication.Login(test.Context(), name, password)
		if err != nil {
			test.Fatal(err)
		}
		sessions[name] = session.Reveal()
		reference, _ := repository.ParseReference("org/"+name, "")
		entry, err := store.CollectRepository(test.Context(), owner, reference, strings.Repeat("b", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Owner skill", Description: "Private source", Path: "skills/owner"}, ContentHash: strings.Repeat("a", 64)}})
		if err != nil {
			test.Fatal(err)
		}
		collected[name], err = store.CollectedRepository(test.Context(), owner, entry.ID)
		if err != nil {
			test.Fatal(err)
		}
	}
	server := httptest.NewServer(NewWithOptions(authentication, Options{Store: store}))
	test.Cleanup(server.Close)
	categories := catalogv1connect.NewCategoryServiceClient(server.Client(), server.URL)
	publication := catalogv1connect.NewPublicationServiceClient(server.Client(), server.URL)
	created := make(map[string]*catalogv1.Category)
	for _, name := range []string{"alice", "bob"} {
		request := connect.NewRequest(&catalogv1.CreateCategoryRequest{Name: "Development"})
		request.Header().Set("Authorization", "Bearer "+sessions[name])
		response, err := categories.CreateCategory(test.Context(), request)
		if err != nil {
			test.Fatal(err)
		}
		created[name] = response.Msg.Category
	}
	list := connect.NewRequest(&catalogv1.ListCategoriesRequest{})
	list.Header().Set("Authorization", "Bearer "+sessions["alice"])
	listed, err := categories.ListCategories(test.Context(), list)
	if err != nil || len(listed.Msg.Categories) != 1 || listed.Msg.Categories[0].Id != created["alice"].Id {
		test.Fatalf("owner list: %+v %v", listed, err)
	}
	update := connect.NewRequest(&catalogv1.UpdateCategoryRequest{CategoryId: created["alice"].Id, ExpectedRevision: 1, Name: "Engineering"})
	update.Header().Set("Authorization", "Bearer "+sessions["bob"])
	if _, err := categories.UpdateCategory(test.Context(), update); connect.CodeOf(err) != connect.CodeNotFound {
		test.Fatalf("foreign update: %v", err)
	}
	set := connect.NewRequest(&catalogv1.SetSkillCategoriesRequest{SkillId: collected["alice"].Skills[0].ID, CategoryIds: []string{created["bob"].Id}})
	set.Header().Set("Authorization", "Bearer "+sessions["alice"])
	if _, err := categories.SetSkillCategories(test.Context(), set); connect.CodeOf(err) != connect.CodeNotFound {
		test.Fatalf("foreign category association: %v", err)
	}
	set.Msg.CategoryIds = []string{created["alice"].Id}
	if _, err := categories.SetSkillCategories(test.Context(), set); err != nil {
		test.Fatal(err)
	}
	get := connect.NewRequest(&catalogv1.GetSkillCategoriesRequest{SkillId: collected["alice"].Skills[0].ID})
	get.Header().Set("Authorization", "Bearer "+sessions["bob"])
	if _, err := categories.GetSkillCategories(test.Context(), get); connect.CodeOf(err) != connect.CodeNotFound {
		test.Fatalf("foreign skill categories: %v", err)
	}
	get.Header().Set("Authorization", "Bearer "+sessions["alice"])
	selected, err := categories.GetSkillCategories(test.Context(), get)
	if err != nil || len(selected.Msg.Categories) != 1 || selected.Msg.Categories[0].SkillCount != 1 {
		test.Fatalf("selected categories: %+v %v", selected, err)
	}
	update.Header().Set("Authorization", "Bearer "+sessions["alice"])
	updated, err := categories.UpdateCategory(test.Context(), update)
	if err != nil || updated.Msg.Category.Revision != 2 || updated.Msg.Category.SkillCount != 1 {
		test.Fatalf("update: %+v %v", updated, err)
	}
	if _, err := categories.UpdateCategory(test.Context(), update); connect.CodeOf(err) != connect.CodeAborted {
		test.Fatalf("stale update: %v", err)
	}
	remove := connect.NewRequest(&catalogv1.DeleteCategoryRequest{CategoryId: created["alice"].Id, ExpectedRevision: 2})
	remove.Header().Set("Authorization", "Bearer "+sessions["bob"])
	if _, err := categories.DeleteCategory(test.Context(), remove); connect.CodeOf(err) != connect.CodeNotFound {
		test.Fatalf("foreign delete: %v", err)
	}
	remove.Header().Set("Authorization", "Bearer "+sessions["alice"])
	remove.Msg.ExpectedRevision = 1
	if _, err := categories.DeleteCategory(test.Context(), remove); connect.CodeOf(err) != connect.CodeAborted {
		test.Fatalf("stale delete: %v", err)
	}
	remove.Msg.ExpectedRevision = 2
	if _, err := categories.DeleteCategory(test.Context(), remove); err != nil {
		test.Fatal(err)
	}
	selected, err = categories.GetSkillCategories(test.Context(), get)
	if err != nil || len(selected.Msg.Categories) != 0 {
		test.Fatalf("deleted category not unlinked: %+v %v", selected, err)
	}
	change := connect.NewRequest(&catalogv1.SetPublicationRequest{RepositoryId: collected["alice"].ID, ExpectedRevision: 1, Published: true, ConfirmFullContents: true})
	change.Header().Set("Authorization", "Bearer "+sessions["bob"])
	if _, err := publication.SetPublication(test.Context(), change); connect.CodeOf(err) != connect.CodeNotFound {
		test.Fatalf("foreign publication: %v", err)
	}
	status := connect.NewRequest(&catalogv1.GetPublicationRequest{RepositoryId: collected["alice"].ID})
	status.Header().Set("Authorization", "Bearer "+sessions["bob"])
	if _, err := publication.GetPublication(test.Context(), status); connect.CodeOf(err) != connect.CodeNotFound {
		test.Fatalf("foreign publication read: %v", err)
	}
	change.Header().Set("Authorization", "Bearer "+sessions["alice"])
	if _, err := publication.SetPublication(test.Context(), change); err != nil {
		test.Fatal(err)
	}
	if _, err := publication.SetPublication(test.Context(), change); connect.CodeOf(err) != connect.CodeAborted {
		test.Fatalf("stale publication: %v", err)
	}
	current, err := store.CollectedRepository(test.Context(), owners["alice"], collected["alice"].ID)
	if err != nil || current.Skills[0].ContentHash != strings.Repeat("a", 64) {
		test.Fatalf("metadata changed content identity: %+v %v", current, err)
	}
}
