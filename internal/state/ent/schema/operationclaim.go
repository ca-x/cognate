package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type OperationClaim struct {
	ent.Schema
}

func (OperationClaim) Fields() []ent.Field {
	return []ent.Field{
		field.String("key").NotEmpty().Unique().Immutable(),
		field.Int("operation_id").Immutable(),
	}
}

func (OperationClaim) Edges() []ent.Edge {
	return []ent.Edge{edge.From("operation", Operation.Type).Ref("claims").Field("operation_id").Unique().Required().Immutable()}
}
