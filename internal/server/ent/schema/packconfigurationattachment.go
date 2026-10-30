package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type PackConfigurationAttachment struct{ ent.Schema }

func (PackConfigurationAttachment) Fields() []ent.Field {
	return []ent.Field{
		field.String("public_id").NotEmpty().Unique().Immutable(),
		field.Int("pack_id").Immutable(),
		field.Int("configuration_asset_id").Immutable(),
		field.Int64("asset_revision").Positive(),
		field.String("target_id").NotEmpty().MaxLen(96).Immutable(),
		field.Int("ordinal").NonNegative().Max(15),
	}
}

func (PackConfigurationAttachment) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("pack", Pack.Type).Ref("configuration_attachments").Field("pack_id").Unique().Required().Immutable(),
		edge.From("configuration_asset", ConfigurationAsset.Type).Ref("pack_attachments").Field("configuration_asset_id").Unique().Required().Immutable(),
	}
}

func (PackConfigurationAttachment) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("pack_id", "configuration_asset_id", "target_id").Unique(),
		index.Fields("pack_id", "ordinal").Unique(),
		index.Fields("configuration_asset_id"),
	}
}
