package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type PackShare struct{ ent.Schema }

func (PackShare) Fields() []ent.Field {
	return []ent.Field{
		field.String("public_id").NotEmpty().Unique().Immutable(),
		field.Int("pack_id").Immutable(),
		field.Bytes("token_hash").NotEmpty().Unique().Immutable().Sensitive(),
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("expires_at").Optional().Nillable().Immutable(),
		field.Time("revoked_at").Optional().Nillable(),
	}
}

func (PackShare) Edges() []ent.Edge {
	return []ent.Edge{edge.From("pack", Pack.Type).Ref("shares").Field("pack_id").Unique().Required().Immutable()}
}

func (PackShare) Indexes() []ent.Index { return []ent.Index{index.Fields("pack_id")} }
