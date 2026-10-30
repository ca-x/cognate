package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type ImportReceipt struct{ ent.Schema }

func (ImportReceipt) Fields() []ent.Field {
	return []ent.Field{
		field.Int("user_id").Immutable(),
		field.Bytes("operation_id").MinLen(16).MaxLen(16).Immutable(),
		field.Bytes("ticket_id").MinLen(16).MaxLen(16).Immutable().Sensitive(),
		field.Bytes("request_digest").MinLen(32).MaxLen(32).Immutable().Sensitive(),
		field.Bytes("origin_digest").MinLen(32).MaxLen(32).Immutable().Sensitive(),
		field.Int64("revision").Positive().Default(1),
		field.Enum("phase").Values("reserved", "collecting", "completed", "partial", "failed"),
		field.Bytes("execution_id").MinLen(16).MaxLen(16).Immutable().Sensitive(),
		field.Time("created_at").Immutable(),
		field.Time("updated_at"),
		field.Bytes("ciphertext").NotEmpty().Sensitive(),
		field.Int64("charge").Min(131072).Max(131072).Immutable(),
	}
}

func (ImportReceipt) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("import_receipts").Field("user_id").Unique().Required().Immutable()}
}

func (ImportReceipt) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "operation_id").Unique(), index.Fields("user_id", "ticket_id").Unique()}
}
