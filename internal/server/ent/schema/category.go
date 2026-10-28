package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Category struct{ ent.Schema }

func (Category) Fields() []ent.Field {
	return []ent.Field{
		field.String("public_id").NotEmpty().Unique().Immutable(),
		field.Int("user_id").Immutable(),
		field.String("name").NotEmpty().MaxLen(80),
		field.String("normalized_name").NotEmpty(),
		field.Int64("revision").Default(1).Positive(),
	}
}

func (Category) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("categories").Field("user_id").Unique().Required().Immutable(),
		edge.To("skills", CollectedSkill.Type),
	}
}

func (Category) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "normalized_name").Unique()}
}
