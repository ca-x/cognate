package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type CollectedRepository struct{ ent.Schema }

func (CollectedRepository) Fields() []ent.Field {
	return []ent.Field{
		field.String("public_id").NotEmpty().Unique().Immutable(),
		field.Int("user_id").Immutable(),
		field.String("name_with_owner").NotEmpty().Immutable(),
		field.String("branch").Default("").Immutable(),
		field.String("archive_hash").NotEmpty(),
		field.Int("skill_count").Positive(),
		field.Bool("manual").Default(false),
		field.Bool("published").Default(false),
		field.Int64("publication_revision").Default(1).Positive(),
		field.Time("collected_at").Default(time.Now),
	}
}

func (CollectedRepository) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("collected_repositories").Field("user_id").Unique().Required().Immutable(),
		edge.To("skills", CollectedSkill.Type),
	}
}

func (CollectedRepository) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "name_with_owner", "branch").Unique(),
		index.Fields("published", "id"),
	}
}
