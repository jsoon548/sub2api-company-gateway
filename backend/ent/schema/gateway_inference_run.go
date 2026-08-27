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

type GatewayInferenceRun struct{ ent.Schema }

func (GatewayInferenceRun) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "gateway_inference_runs"}}
}

func (GatewayInferenceRun) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.String("purpose").MaxLen(64).NotEmpty().Immutable(),
		field.String("profile").MaxLen(64).NotEmpty().Immutable(),
		field.String("backend").MaxLen(100).NotEmpty().Immutable(),
		field.String("provider").MaxLen(100).NotEmpty().Immutable(),
		field.String("model").MaxLen(100).NotEmpty().Immutable(),
		field.String("prompt_version").MaxLen(64).NotEmpty().Immutable(),
		field.String("schema_version").MaxLen(64).NotEmpty().Immutable(),
		field.Enum("status").Values(
			"completed", "invalid_request", "unavailable", "timeout", "canceled", "rejected",
			"rate_limited", "provider_error", "refused", "empty_response", "invalid_response",
			"response_too_large", "usage_missing",
		).Immutable(),
		field.String("provider_request_id").MaxLen(255).Optional().Nillable().Immutable(),
		field.Int64("input_units").Optional().Nillable().Immutable(),
		field.Int64("output_units").Optional().Nillable().Immutable(),
		field.Int64("latency_ms").Default(0).Immutable(),
		field.Time("created_at").Default(time.Now).Immutable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (GatewayInferenceRun) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("profile", "created_at").StorageKey("gateway_inference_runs_profile_created_idx"),
		index.Fields("status", "created_at").StorageKey("gateway_inference_runs_status_created_idx"),
	}
}
