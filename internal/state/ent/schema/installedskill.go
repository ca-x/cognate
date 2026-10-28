package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type InstalledSkill struct {
	ent.Schema
}

func (InstalledSkill) Fields() []ent.Field {
	return []ent.Field{
		field.String("slug").NotEmpty().Unique(),
		field.String("content_hash").NotEmpty(),
		field.String("generation").Default(""),
		field.Bool("subscription_managed").Default(false),
	}
}

func (InstalledSkill) Edges() []ent.Edge {
	return []ent.Edge{edge.To("projections", Projection.Type)}
}
