package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type NativeMaterializationGrant struct{ ent.Schema }

func (NativeMaterializationGrant) Fields() []ent.Field {
	return []ent.Field{
		field.String("public_id").NotEmpty().MaxLen(64).Unique().Immutable(),
		field.Int("user_id").Immutable(),
		field.Int("native_session_id").Immutable(),
		field.Int("prepare_id").Unique().Immutable(),
		field.Bytes("token_hash").MinLen(32).MaxLen(32).Unique().Immutable().Sensitive(),
		field.Bytes("consume_challenge").MinLen(32).MaxLen(32).Immutable().Sensitive(),
		field.Bytes("binding_digest").MinLen(32).MaxLen(32).Immutable().Sensitive(),
		field.Int64("issued_ms").Positive().Immutable(),
		field.Int64("expires_ms").Positive().Immutable(),
		field.Int64("consumed_ms").NonNegative().Default(0),
		field.Bool("retired").Default(false),
		field.Bool("expired").Default(false),
		field.Int64("charge").Min(512).Max(4 << 10).Immutable(),
	}
}

func (NativeMaterializationGrant) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("native_materialization_grants").Field("user_id").Unique().Required().Immutable(),
		edge.From("native_session", NativeConsumerSession.Type).Ref("grants").Field("native_session_id").Unique().Required().Immutable(),
		edge.From("prepare", NativeApplyPreparation.Type).Ref("grant").Field("prepare_id").Unique().Required().Immutable(),
	}
}

func (NativeMaterializationGrant) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "native_session_id")}
}
