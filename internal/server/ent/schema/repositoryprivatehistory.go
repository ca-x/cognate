package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type RepositoryPrivateHistory struct{ ent.Schema }

func (RepositoryPrivateHistory) Fields() []ent.Field {
	return []ent.Field{
		field.Int("user_id").Immutable(),
		field.String("name_with_owner").NotEmpty().Immutable(),
	}
}

func (RepositoryPrivateHistory) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("repository_private_history").Field("user_id").Unique().Required().Immutable(),
	}
}

func (RepositoryPrivateHistory) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "name_with_owner").Unique()}
}
