package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type SubscriptionSkill struct {
	ent.Schema
}

func (SubscriptionSkill) Fields() []ent.Field {
	return []ent.Field{
		field.Int("subscription_id"),
		field.String("slug").NotEmpty().Immutable(),
		field.String("desired_hash").Default(""),
		field.String("applied_hash").Default(""),
	}
}

func (SubscriptionSkill) Indexes() []ent.Index {
	return []ent.Index{index.Fields("subscription_id", "slug").Unique()}
}

func (SubscriptionSkill) Edges() []ent.Edge {
	return []ent.Edge{edge.From("subscription", Subscription.Type).Ref("skills").Field("subscription_id").Unique().Required()}
}
