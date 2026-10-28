package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type GitHubMembership struct{ ent.Schema }

func (GitHubMembership) Fields() []ent.Field {
	return []ent.Field{
		field.Int("list_id").Immutable(),
		field.Int("repository_id").Immutable(),
		field.Bool("visible").Default(true),
	}
}

func (GitHubMembership) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("list", GitHubList.Type).Ref("memberships").Field("list_id").Unique().Required().Immutable(),
		edge.From("repository", GitHubRepository.Type).Ref("memberships").Field("repository_id").Unique().Required().Immutable(),
	}
}

func (GitHubMembership) Indexes() []ent.Index {
	return []ent.Index{index.Fields("list_id", "repository_id").Unique()}
}
