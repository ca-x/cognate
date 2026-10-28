package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type PackItem struct{ ent.Schema }

func (PackItem) Fields() []ent.Field {
	return []ent.Field{
		field.Int("pack_id").Immutable(),
		field.Int("skill_id").Immutable(),
		field.String("slug").NotEmpty(),
	}
}

func (PackItem) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("pack", Pack.Type).Ref("items").Field("pack_id").Unique().Required().Immutable(),
		edge.From("skill", CollectedSkill.Type).Ref("pack_items").Field("skill_id").Unique().Required().Immutable(),
	}
}

func (PackItem) Indexes() []ent.Index { return []ent.Index{index.Fields("pack_id", "skill_id").Unique()} }
