package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type MCPCredential struct{ ent.Schema }

func (MCPCredential) Fields() []ent.Field {
	return []ent.Field{
		field.Int("user_id").Immutable(),
		field.String("public_id").NotEmpty().Unique().Immutable(),
		field.String("name").NotEmpty().MaxLen(128),
		field.Bytes("token_hash").NotEmpty().Unique().Sensitive(),
		field.Strings("scopes"),
		field.Time("created_at").Immutable(),
		field.Time("expires_at"),
		field.Time("revoked_at").Optional().Nillable(),
		field.Int64("revision").Default(1).Positive(),
	}
}

func (MCPCredential) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).Ref("mcp_credentials").Field("user_id").Unique().Required().Immutable(),
		edge.To("audits", MCPAudit.Type),
	}
}

func (MCPCredential) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "created_at")}
}
