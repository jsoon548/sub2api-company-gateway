package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

type RouteDecision struct{ ent.Schema }

func (RouteDecision) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "route_decisions"}}
}

func (RouteDecision) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.UUID("gateway_request_id", uuid.UUID{}).Unique().Immutable(),
		field.UUID("work_session_id", uuid.UUID{}).Immutable(),
		field.Int64("employee_user_id").Immutable(),
		field.String("profile_version").MaxLen(64).Immutable(),
		field.Int64("config_version").Immutable(),
		field.JSON("required_capabilities", []string{}).Default([]string{}).Immutable(),
		field.Enum("task_complexity").Values("simple", "general", "complex").Immutable(),
		field.Enum("certainty").Values("deterministic", "decisive", "uncertain").Immutable(),
		field.String("explanation").MaxLen(1024).Immutable(),
		field.Enum("decision_source").Values("rule", "classifier", "fallback").Immutable(),
		field.String("rule_version").MaxLen(64).Immutable(),
		field.UUID("classifier_run_id", uuid.UUID{}).Optional().Nillable().Unique().Immutable(),
		field.String("classifier_version").MaxLen(64).Optional().Nillable().Immutable(),
		field.Enum("classifier_status").Values("not_called", "completed", "timeout", "invalid", "unavailable").Immutable(),
		field.Int64("classifier_latency_ms").Default(0).Immutable(),
		field.Enum("requested_tier").Values("economy", "general", "advanced").Immutable(),
		field.Enum("effective_tier").Values("economy", "general", "advanced").Immutable(),
		field.JSON("candidate_pool", []map[string]any{}).Default([]map[string]any{}).Immutable(),
		field.String("actual_logical_model").MaxLen(100).Optional().Nillable().Immutable(),
		field.String("actual_provider_model").MaxLen(100).Optional().Nillable().Immutable(),
		field.String("change_reason").MaxLen(64).Immutable(),
		field.Int16("technical_retry_count").Default(0),
		field.String("technical_retry_reason").MaxLen(64).Optional().Nillable(),
		field.Enum("decision_result").Values("selected", "unavailable", "failed"),
		field.Int64("routing_latency_ms").Default(0).Immutable(),
		field.Time("created_at").Default(time.Now).Immutable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (RouteDecision) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("work_session_id", "created_at").StorageKey("route_decisions_session_created_idx"),
		index.Fields("employee_user_id", "created_at").StorageKey("route_decisions_employee_created_idx"),
		index.Fields("classifier_status", "classifier_latency_ms").StorageKey("route_decisions_classifier_latency_idx").Annotations(entsql.IndexWhere("classifier_status <> 'not_called'")),
	}
}
