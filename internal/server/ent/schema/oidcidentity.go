package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type OIDCIdentity struct{ ent.Schema }

func (OIDCIdentity) Fields() []ent.Field {
	return []ent.Field{
		field.Int("user_id").Immutable(),
		field.String("issuer").NotEmpty().Immutable().Sensitive(),
		field.String("subject").NotEmpty().Immutable().Sensitive(),
	}
}

func (OIDCIdentity) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("oidc_identities").Field("user_id").Unique().Required().Immutable()}
}

func (OIDCIdentity) Indexes() []ent.Index {
	return []ent.Index{index.Fields("issuer", "subject").Unique()}
}
