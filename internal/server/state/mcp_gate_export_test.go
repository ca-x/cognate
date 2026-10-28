package state

import (
	"context"
	"time"

	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/collectedrepository"
)

func (store *Store) MCPTestInterceptCredentials(interceptor ent.Interceptor) {
	store.state.client.MCPCredential.Intercept(interceptor)
}

func (store *Store) MCPTestClock(now func() time.Time) {
	store.state.now = now
}

func (store *Store) MCPTestCategoryHook(hook ent.Hook) {
	store.state.client.Category.Use(hook)
}

func (store *Store) MCPTestPublicationHook(hook ent.Hook) {
	store.state.client.CollectedRepository.Use(hook)
}

func (store *Store) MCPTestPackHook(hook ent.Hook) {
	store.state.client.Pack.Use(hook)
}

func (store *Store) MCPTestPackItemHook(hook ent.Hook) {
	store.state.client.PackItem.Use(hook)
}

func (store *Store) MCPTestSkillHook(hook ent.Hook) {
	store.state.client.CollectedSkill.Use(hook)
}

func (store *Store) MCPTestRepositoryManual(ctx context.Context, id string, manual bool) error {
	return store.state.client.CollectedRepository.Update().Where(collectedrepository.PublicIDEQ(id)).SetManual(manual).Exec(ctx)
}
