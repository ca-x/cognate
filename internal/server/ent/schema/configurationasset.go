package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type ConfigurationAsset struct{ ent.Schema }

func (ConfigurationAsset) Fields() []ent.Field {
	return []ent.Field{
		field.String("public_id").NotEmpty().Unique().Immutable(),
		field.Int("user_id").Immutable(),
		field.String("agent_id").NotEmpty().MaxLen(64).Immutable(),
		field.Enum("kind").Values("provider", "mcp", "prompt", "agent-settings", "workspace").Immutable(),
		field.String("name").NotEmpty().MaxLen(128),
		field.Int("schema_version").Positive().Max(65535),
		field.Int64("revision").Positive().Default(1),
		field.Time("created_at").Immutable(),
		field.Time("updated_at"),
		field.Bytes("ciphertext").NotEmpty().Sensitive(),
	}
}

func (ConfigurationAsset) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("configuration_assets").Field("user_id").Unique().Required().Immutable(),
		edge.To("pack_attachments", PackConfigurationAttachment.Type),
	}
}

func (ConfigurationAsset) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "created_at")}
}
