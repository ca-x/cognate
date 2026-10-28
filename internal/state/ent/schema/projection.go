package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type Projection struct {
	ent.Schema
}

func (Projection) Fields() []ent.Field {
	return []ent.Field{
		field.String("path").NotEmpty(),
		field.String("path_key").NotEmpty().Unique(),
		field.String("kind").NotEmpty(),
		field.String("identity").Default(""),
	}
}

func (Projection) Edges() []ent.Edge {
	return []ent.Edge{edge.From("skill", InstalledSkill.Type).Ref("projections").Unique().Required()}
}
