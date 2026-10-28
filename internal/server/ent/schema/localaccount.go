package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type LocalAccount struct{ ent.Schema }

func (LocalAccount) Fields() []ent.Field {
	return []ent.Field{
		field.Int("user_id").Unique().Immutable(),
		field.String("username").NotEmpty().Unique().Immutable(),
		field.Bytes("password_hash").NotEmpty().Sensitive(),
	}
}

func (LocalAccount) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("local_account").Field("user_id").Unique().Required().Immutable()}
}
