package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type GitHubCredential struct {
	ent.Schema
}

func (GitHubCredential) Fields() []ent.Field {
	return []ent.Field{
		field.Int("user_id").Unique().Immutable(),
		field.String("viewer_id").NotEmpty(),
		field.String("client_id").Default(""),
		field.String("key_id").NotEmpty(),
		field.String("generation").NotEmpty(),
		field.Bytes("ciphertext").NotEmpty().Sensitive(),
		field.Enum("status").Values("ready", "refreshing", "needs_reauthorization").Default("ready"),
		field.String("refresh_attempt").Default(""),
		field.Int64("sync_sequence").NonNegative().Default(0),
		field.Int64("sync_applied_sequence").NonNegative().Default(0),
		field.String("sync_fetch_id").Default(""),
		field.Int("collection_cursor").NonNegative().Default(0),
	}
}

func (GitHubCredential) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("github_credential").Field("user_id").Unique().Required().Immutable()}
}
