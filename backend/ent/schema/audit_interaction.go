package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// AuditInteraction is the PostgreSQL metadata record for one future audited
// Gateway request. audit foundation defines storage only; no Gateway path creates it yet.
type AuditInteraction struct{ ent.Schema }

func (AuditInteraction) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "audit_interactions"}}
}

func (AuditInteraction) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.UUID("gateway_request_id", uuid.UUID{}).Unique().Immutable(),
		field.UUID("work_session_id", uuid.UUID{}).Optional().Nillable(),
		field.Int64("subject_user_id").Optional().Nillable().Immutable(),
		field.String("subject_email_snapshot").MaxLen(255).Optional().Nillable().Immutable(),
		field.Int64("api_key_id").Optional().Nillable().Immutable(),
		field.String("api_key_fingerprint").MaxLen(64).Optional().Nillable().Immutable(),
		field.String("profile_version").MaxLen(64).Immutable(),
		field.Enum("protocol").Values("anthropic", "openai").Immutable(),
		field.String("endpoint").MaxLen(512).Immutable(),
		field.String("method").MaxLen(16).Immutable(),
		field.Enum("transport").Values("http", "sse").Immutable(),
		field.String("requested_model").MaxLen(100).Optional().Nillable().Immutable(),
		field.String("resolved_model").MaxLen(100).Optional().Nillable(),
		field.Enum("request_outcome").Values("processing", "rejected_pre_upstream", "completed", "upstream_failed", "interrupted").Default("processing"),
		field.Int64("request_outcome_version").Default(0),
		field.Enum("content_state").Values("recording", "complete", "incomplete", "expired").Default("recording"),
		field.Int64("content_state_version").Default(0),
		field.Int16("downstream_status").Optional().Nillable(),
		field.Enum("downstream_write_result").Values("not_applicable", "pending", "succeeded", "failed", "unknown").Default("not_applicable"),
		field.Time("admitted_at").Immutable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("completed_at").Optional().Nillable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("expires_at").Immutable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("last_activity_at").SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Bytes("request_sha256").Optional().Nillable(),
		field.Bytes("response_sha256").Optional().Nillable(),
		field.Int("request_part_count").Default(0),
		field.Int("response_part_count").Default(0),
		field.String("safe_error_summary").MaxLen(512).Optional().Nillable(),
		field.Time("created_at").Default(time.Now).Immutable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (AuditInteraction) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("content_parts", AuditContentPart.Type),
	}
}

func (AuditInteraction) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("subject_user_id", "admitted_at").StorageKey("audit_interactions_subject_admitted_idx").Annotations(entsql.IndexWhere("subject_user_id IS NOT NULL")),
		index.Fields("request_outcome", "content_state", "last_activity_at").StorageKey("audit_interactions_outcome_state_activity_idx"),
		index.Fields("expires_at").StorageKey("audit_interactions_expires_idx"),
		index.Fields("work_session_id", "admitted_at").StorageKey("audit_interactions_work_session_idx").Annotations(entsql.IndexWhere("work_session_id IS NOT NULL")),
	}
}
