package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type NativeApplyPreparation struct{ ent.Schema }

func (NativeApplyPreparation) Fields() []ent.Field {
	return []ent.Field{
		field.String("public_id").NotEmpty().MaxLen(64).Unique().Immutable(),
		field.Int("user_id").Immutable(),
		field.Int("native_session_id").Immutable(),
		field.Bytes("operation_id").MinLen(16).MaxLen(16).Immutable().Sensitive(),
		field.Bytes("request_digest").MinLen(32).MaxLen(32).Immutable().Sensitive(),
		field.Bytes("authorize_challenge").MinLen(32).MaxLen(32).Immutable().Sensitive(),
		field.Bytes("ciphertext").NotEmpty().MaxLen(16 << 10).Sensitive(),
		field.Enum("phase").Values("patch-ready", "bound-with-grant", "bound-no-secret", "expired", "retired").Default("patch-ready"),
		field.Int64("issued_ms").Positive().Immutable(),
		field.Int64("expires_ms").Positive().Immutable(),
		field.Int64("charge").Min(512).Max(16 << 10),
	}
}

func (NativeApplyPreparation) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("native_apply_preparations").Field("user_id").Unique().Required().Immutable(),
		edge.From("native_session", NativeConsumerSession.Type).Ref("preparations").Field("native_session_id").Unique().Required().Immutable(),
		edge.To("grant", NativeMaterializationGrant.Type).Unique().Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (NativeApplyPreparation) Indexes() []ent.Index {
	return []ent.Index{index.Fields("native_session_id", "operation_id").Unique(), index.Fields("user_id", "native_session_id")}
}
