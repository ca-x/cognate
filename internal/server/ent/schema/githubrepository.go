package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type GitHubRepository struct{ ent.Schema }

func (GitHubRepository) Fields() []ent.Field {
	return []ent.Field{
		field.Int("user_id").Immutable(),
		field.String("viewer_id").NotEmpty().Immutable(),
		field.String("repository_key").NotEmpty().Immutable(),
		field.String("name_with_owner").NotEmpty(),
		field.Int64("database_id").Optional().Nillable(),
		field.Bool("private"),
	}
}

func (GitHubRepository) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("github_repositories").Field("user_id").Unique().Required().Immutable(),
		edge.To("memberships", GitHubMembership.Type),
	}
}

func (GitHubRepository) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "viewer_id", "repository_key").Unique()}
}
