package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

type ServerMetadata struct {
	ent.Schema
}

func (ServerMetadata) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty().Unique().Immutable(),
		field.String("key_id").NotEmpty().Immutable(),
		field.Bytes("key_proof").NotEmpty().Immutable().Sensitive(),
	}
}
