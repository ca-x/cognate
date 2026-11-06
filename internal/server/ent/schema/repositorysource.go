package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/ca-x/cognate/internal/repository"
)

type RepositorySource struct{ ent.Schema }

func (RepositorySource) Fields() []ent.Field {
	return []ent.Field{
		field.String("public_id").NotEmpty().Unique().Immutable(),
		field.Int("user_id").Immutable(),
		field.String("name_with_owner").NotEmpty().Immutable(),
		field.String("branch").Default("").Immutable(),
		field.JSON("skills", []repository.SkillSource{}),
		field.Time("registered_at").Default(time.Now).Immutable(),
	}
}

func (RepositorySource) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("repository_sources").Field("user_id").Unique().Required().Immutable(),
	}
}

func (RepositorySource) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "name_with_owner", "branch").Unique()}
}
