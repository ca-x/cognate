package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type OperationTarget struct {
	ent.Schema
}

func (OperationTarget) Fields() []ent.Field {
	return []ent.Field{
		field.Int("operation_id").Immutable(),
		field.Int("position").NonNegative().Immutable(),
		field.String("path").NotEmpty().Immutable(),
		field.String("path_key").NotEmpty().Immutable(),
		field.String("stage_name").Default("").Immutable(),
		field.String("expected_identity").Default("").Immutable(),
		field.String("stage_identity").Default(""),
		field.String("object_identity").Default(""),
		field.Bool("stage_ready").Default(false),
		field.Bool("publish_intent").Default(false),
		field.Bool("published").Default(false),
		field.Bool("cleanup_intent").Default(false),
		field.Bool("cleaned").Default(false),
		field.Enum("kind").Values("canonical", "symlink", "junction", "copy", "backup", "backup_source", "restore_destination").Immutable(),
	}
}

func (OperationTarget) Edges() []ent.Edge {
	return []ent.Edge{edge.From("operation", Operation.Type).Ref("targets").Field("operation_id").Unique().Required().Immutable()}
}

func (OperationTarget) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("operation_id", "path_key").Unique(),
		index.Fields("operation_id", "position").Unique(),
	}
}
