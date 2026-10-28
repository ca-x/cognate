package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type Session struct{ ent.Schema }

func (Session) Fields() []ent.Field {
	return []ent.Field{
		field.Int("user_id").Immutable(),
		field.Bytes("token_hash").NotEmpty().Unique().Immutable().Sensitive(),
		field.Time("issued_at").Immutable(),
		field.Time("expires_at").Immutable(),
	}
}

func (Session) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("sessions").Field("user_id").Unique().Required().Immutable()}
}
