package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type MCPAudit struct{ ent.Schema }

func (MCPAudit) Fields() []ent.Field {
	return []ent.Field{
		field.Int("credential_id").Immutable(),
		field.String("action").NotEmpty().MaxLen(64).Immutable(),
		field.String("resource").MaxLen(26).Immutable(),
		field.Enum("outcome").Values("success", "denied", "failure").Immutable(),
		field.Time("created_at").Immutable(),
	}
}

func (MCPAudit) Edges() []ent.Edge {
	return []ent.Edge{edge.From("credential", MCPCredential.Type).Ref("audits").Field("credential_id").Unique().Required().Immutable()}
}

func (MCPAudit) Indexes() []ent.Index {
	return []ent.Index{index.Fields("credential_id", "id")}
}
