package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type ConfigurationOrder struct{ ent.Schema }

func (ConfigurationOrder) Fields() []ent.Field {
	return []ent.Field{
		field.Int("user_id").Immutable(),
		field.String("agent_id").NotEmpty().MaxLen(64).Immutable(),
		field.Enum("kind").Values("provider", "mcp", "prompt", "agent-settings", "workspace").Immutable(),
		field.Int64("revision").Positive().Default(1),
	}
}

func (ConfigurationOrder) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("user", User.Type).Field("user_id").Unique().Required().Immutable().Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (ConfigurationOrder) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "agent_id", "kind").Unique()}
}
