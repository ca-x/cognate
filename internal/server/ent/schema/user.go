package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type User struct {
	ent.Schema
}

func (User) Fields() []ent.Field {
	return []ent.Field{field.String("user_key").NotEmpty().Unique().Immutable()}
}

func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("github_credential", GitHubCredential.Type).Unique(),
		edge.To("github_lists", GitHubList.Type),
		edge.To("github_repositories", GitHubRepository.Type),
		edge.To("local_account", LocalAccount.Type).Unique(),
		edge.To("sessions", Session.Type),
		edge.To("oidc_identities", OIDCIdentity.Type),
		edge.To("collected_repositories", CollectedRepository.Type),
		edge.To("packs", Pack.Type),
		edge.To("mcp_credentials", MCPCredential.Type),
		edge.To("categories", Category.Type),
		edge.To("configuration_assets", ConfigurationAsset.Type),
		edge.To("native_consumer_challenges", NativeConsumerChallenge.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("native_consumer_sessions", NativeConsumerSession.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}
