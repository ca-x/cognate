package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Pack struct{ ent.Schema }

func (Pack) Fields() []ent.Field {
	return []ent.Field{
		field.String("public_id").NotEmpty().Unique().Immutable(),
		field.Int("user_id").Immutable(),
		field.String("name").NotEmpty(),
		field.String("description").Default(""),
		field.Int64("revision").Positive().Default(1),
		field.Int("item_count").Positive(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now),
	}
}

func (Pack) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("packs").Field("user_id").Unique().Required().Immutable(),
		edge.To("items", PackItem.Type),
		edge.To("shares", PackShare.Type),
	}
}

func (Pack) Indexes() []ent.Index { return []ent.Index{index.Fields("user_id")} }
