package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// GovernanceEvent is append-only evidence for governance and lifecycle actions.
// PostgreSQL triggers in migration 173 reject UPDATE and DELETE operations.
type GovernanceEvent struct{ ent.Schema }

func (GovernanceEvent) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "governance_events"}}
}

func (GovernanceEvent) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.UUID("operation_id", uuid.UUID{}).Immutable(),
		field.Int("event_sequence").Immutable(),
		field.Enum("actor_kind").Values("named_admin", "deployment_operator", "system").SchemaType(map[string]string{dialect.Postgres: "governance_actor_kind"}).Immutable(),
		field.Int64("actor_user_id").Optional().Nillable().Immutable(),
		field.String("deployment_operator_id").MaxLen(255).Optional().Nillable().Immutable(),
		field.String("target_kind").MaxLen(64).Immutable(),
		field.String("target_id").MaxLen(255).Immutable(),
		field.String("action").MaxLen(64).Immutable(),
		field.Enum("result").Values("started", "succeeded", "failed", "rejected", "reconciled").SchemaType(map[string]string{dialect.Postgres: "governance_result"}).Immutable(),
		field.String("reason").MaxLen(512).Optional().Nillable().Immutable(),
		field.JSON("before_summary", map[string]any{}).Optional().Immutable(),
		field.JSON("after_summary", map[string]any{}).Optional().Immutable(),
		field.String("recovery_nonce_fingerprint").MaxLen(64).Optional().Nillable().Immutable(),
		field.String("gateway_request_id").MaxLen(36).Optional().Nillable().Immutable(),
		field.String("safe_error_summary").MaxLen(512).Optional().Nillable().Immutable(),
		field.Time("occurred_at").Immutable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (GovernanceEvent) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("actor", User.Type).
			Field("actor_user_id").
			Unique().
			Immutable().
			Annotations(entsql.OnDelete(entsql.Restrict)),
	}
}

func (GovernanceEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("operation_id", "event_sequence").Unique(),
		index.Fields("recovery_nonce_fingerprint").Unique(),
		index.Fields("target_kind", "target_id", "occurred_at").StorageKey("governance_target_time_idx"),
		index.Fields("actor_user_id", "occurred_at").StorageKey("governance_actor_time_idx").Annotations(entsql.IndexWhere("actor_user_id IS NOT NULL")),
	}
}
