package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

type Backup struct {
	ent.Schema
}

func (Backup) Fields() []ent.Field {
	return []ent.Field{
		field.String("backup_key").NotEmpty().Unique().Immutable(),
		field.String("slug").NotEmpty().Immutable(),
		field.String("format").NotEmpty().Immutable(),
		field.String("source_path").NotEmpty().Immutable(),
		field.String("source_identity").NotEmpty().Immutable(),
		field.String("installation_generation").Default("").Immutable(),
		field.String("installation_hash").NotEmpty().Immutable(),
		field.String("content_hash").NotEmpty().Immutable(),
		field.String("mode_hash").NotEmpty().Immutable(),
		field.Int("root_mode").Immutable(),
		field.String("path").NotEmpty().Immutable(),
		field.String("path_key").NotEmpty().Unique().Immutable(),
		field.String("identity").NotEmpty().Immutable(),
	}
}
