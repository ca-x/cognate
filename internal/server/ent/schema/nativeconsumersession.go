package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type NativeConsumerSession struct{ ent.Schema }

func (NativeConsumerSession) Fields() []ent.Field {
	return []ent.Field{
		field.String("public_id").NotEmpty().MaxLen(64).Unique().Immutable(),
		field.Int("user_id").Immutable(),
		field.Int("parent_session_id").Immutable(),
		field.Enum("purpose").Values("configuration-apply").Immutable(),
		field.String("origin").NotEmpty().MaxLen(2048).Immutable(),
		field.Bytes("operation_id").MinLen(16).MaxLen(16).Immutable().Sensitive(),
		field.Bytes("public_key").MinLen(32).MaxLen(32).Immutable().Sensitive(),
		field.Bytes("session_binding").MinLen(32).MaxLen(32).Unique().Immutable().Sensitive(),
		field.Bytes("token_hash").MinLen(32).MaxLen(32).Unique().Immutable().Sensitive(),
		field.Int64("issued_ms").Positive().Immutable(),
		field.Int64("expires_ms").Positive().Immutable(),
		field.Int64("gate_version").NonNegative().Default(0),
		field.Int64("charge").Min(512).Max(4096).Immutable(),
	}
}

func (NativeConsumerSession) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("native_consumer_sessions").Field("user_id").Unique().Required().Immutable(),
		edge.From("parent_session", Session.Type).Ref("native_consumer_sessions").Field("parent_session_id").Unique().Required().Immutable(),
		edge.To("preparations", NativeApplyPreparation.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("grants", NativeMaterializationGrant.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (NativeConsumerSession) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "parent_session_id"), index.Fields("expires_ms", "id"), index.Fields("parent_session_id", "operation_id").Unique()}
}
