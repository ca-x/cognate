package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type Subscription struct {
	ent.Schema
}

func (Subscription) Fields() []ent.Field {
	return []ent.Field{
		field.String("subscription_key").NotEmpty().Unique().Immutable(),
		field.String("server").NotEmpty().Immutable(),
		field.String("pack_id").NotEmpty().Immutable(),
		field.String("name").NotEmpty(),
		field.Bytes("sealed_credential").NotEmpty().Sensitive(),
		field.Int64("version").Default(1).Positive(),
		field.Bool("enabled").Default(true),
		field.Int64("desired_revision").Default(0).NonNegative(),
		field.String("desired_manifest_hash").Default(""),
		field.Int64("applied_revision").Default(0).NonNegative(),
		field.String("applied_manifest_hash").Default(""),
	}
}

func (Subscription) Indexes() []ent.Index {
	return []ent.Index{index.Fields("server", "pack_id").Unique()}
}

func (Subscription) Edges() []ent.Edge {
	return []ent.Edge{edge.To("skills", SubscriptionSkill.Type)}
}
