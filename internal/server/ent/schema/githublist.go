package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type GitHubList struct{ ent.Schema }

func (GitHubList) Fields() []ent.Field {
	return []ent.Field{
		field.Int("user_id").Immutable(),
		field.String("viewer_id").NotEmpty().Immutable(),
		field.String("list_key").NotEmpty().Immutable(),
		field.String("credential_generation").Default(""),
		field.String("name"),
		field.String("slug").NotEmpty(),
		field.Bool("private"),
		field.Time("updated_at"),
		field.Time("last_added_at"),
		field.Time("verified_at").Optional().Nillable(),
		field.Time("items_verified_at").Optional().Nillable(),
		field.Bool("subscribed").Default(false),
		field.Bool("visible").Default(true),
	}
}

func (GitHubList) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("github_lists").Field("user_id").Unique().Required().Immutable(),
		edge.To("memberships", GitHubMembership.Type),
	}
}

func (GitHubList) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "viewer_id", "list_key").Unique()}
}
