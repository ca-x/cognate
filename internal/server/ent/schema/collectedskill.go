package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type CollectedSkill struct{ ent.Schema }

func (CollectedSkill) Fields() []ent.Field {
	return []ent.Field{
		field.String("public_id").NotEmpty().Unique().Immutable(),
		field.Int("repository_id").Immutable(),
		field.String("path").NotEmpty().Immutable(),
		field.String("name").NotEmpty(),
		field.String("description").NotEmpty(),
		field.String("content_hash").NotEmpty(),
	}
}

func (CollectedSkill) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("repository", CollectedRepository.Type).Ref("skills").Field("repository_id").Unique().Required().Immutable(),
		edge.To("pack_items", PackItem.Type),
		edge.From("categories", Category.Type).Ref("skills"),
	}
}

func (CollectedSkill) Indexes() []ent.Index {
	return []ent.Index{index.Fields("repository_id", "path").Unique()}
}
