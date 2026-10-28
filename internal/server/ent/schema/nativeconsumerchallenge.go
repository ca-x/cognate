package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type NativeConsumerChallenge struct{ ent.Schema }

func (NativeConsumerChallenge) Fields() []ent.Field {
	return []ent.Field{
		field.String("public_id").NotEmpty().MaxLen(64).Unique().Immutable(),
		field.Int("user_id").Immutable(),
		field.Int("parent_session_id").Immutable(),
		field.Enum("purpose").Values("configuration-apply").Immutable(),
		field.String("origin").NotEmpty().MaxLen(2048).Immutable(),
		field.Bytes("operation_id").MinLen(16).MaxLen(16).Immutable().Sensitive(),
		field.Bytes("public_key").MinLen(32).MaxLen(32).Immutable().Sensitive(),
		field.Bytes("client_nonce").MinLen(32).MaxLen(32).Immutable().Sensitive(),
		field.Bytes("challenge").MinLen(32).MaxLen(32).Immutable().Sensitive(),
		field.Bytes("session_binding").MinLen(32).MaxLen(32).Unique().Immutable().Sensitive(),
		field.Int64("issued_ms").Positive().Immutable(),
		field.Int64("expires_ms").Positive().Immutable(),
		field.Int64("native_expires_ms").Positive().Immutable(),
		field.Bool("used").Default(false),
		field.Int64("charge").Min(512).Max(4096).Immutable(),
	}
}

func (NativeConsumerChallenge) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("native_consumer_challenges").Field("user_id").Unique().Required().Immutable(),
		edge.From("parent_session", Session.Type).Ref("native_consumer_challenges").Field("parent_session_id").Unique().Required().Immutable(),
	}
}

func (NativeConsumerChallenge) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "parent_session_id"), index.Fields("used", "id"), index.Fields("expires_ms", "id")}
}
