package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type ConfigurationOrderEntry struct{ ent.Schema }

func (ConfigurationOrderEntry) Fields() []ent.Field {
	return []ent.Field{
		field.Int("order_id").Immutable(),
		field.Int("asset_id").Immutable(),
		field.Int("ordinal").NonNegative().Max(249),
	}
}

func (ConfigurationOrderEntry) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("order", ConfigurationOrder.Type).Field("order_id").Unique().Required().Immutable().Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("asset", ConfigurationAsset.Type).Field("asset_id").Unique().Required().Immutable().Annotations(entsql.OnDelete(entsql.NoAction)),
	}
}

func (ConfigurationOrderEntry) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("order_id", "asset_id").Unique(),
		index.Fields("order_id", "ordinal").Unique(),
		index.Fields("asset_id").Unique(),
	}
}
