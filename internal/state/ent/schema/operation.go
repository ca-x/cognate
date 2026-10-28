package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

type Operation struct {
	ent.Schema
}

func (Operation) Fields() []ent.Field {
	return []ent.Field{
		field.String("operation_key").NotEmpty().Unique().Immutable(),
		field.Enum("action").Values("install", "update", "uninstall", "backup", "restore").Immutable(),
		field.String("slug").NotEmpty().Immutable(),
		field.String("content_hash").NotEmpty().Immutable(),
		field.Bool("subscription_managed").Default(false).Immutable(),
		field.String("backup_format").Default("").Immutable(),
		field.String("backup_id").Default("").Immutable(),
		field.String("backup_mode_hash").Default("").Immutable(),
		field.Int("backup_root_mode").Default(0).Immutable(),
		field.String("backup_source_identity").Default("").Immutable(),
		field.String("backup_installation_generation").Default("").Immutable(),
		field.String("backup_installation_hash").Default("").Immutable(),
		field.Enum("status").Values("planned", "active", "needs_attention", "completed", "cancelled").Default("planned"),
	}
}

func (Operation) Edges() []ent.Edge {
	return []ent.Edge{edge.To("targets", OperationTarget.Type), edge.To("claims", OperationClaim.Type)}
}
