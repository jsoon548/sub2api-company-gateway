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

type WorkSession struct{ ent.Schema }

func (WorkSession) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "work_sessions"}}
}

func (WorkSession) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.String("tenant_id").MaxLen(128).Immutable(),
		field.Int64("employee_user_id").Immutable(),
		field.String("profile_version").MaxLen(64).Immutable(),
		field.String("signal_source").MaxLen(64).Immutable(),
		field.Enum("signal_status").Values("verified", "missing", "malformed").Immutable(),
		field.Bytes("session_key_hmac").Optional().Nillable().Immutable(),
		field.String("hmac_key_version").MaxLen(64).Optional().Nillable().Immutable(),
		field.Enum("reliability").Values("reliable", "unreliable").Immutable(),
		field.Enum("routing_mode").Values("explicit", "auto").Immutable(),
		field.Int64("config_version").Immutable(),
		field.Bool("analysis_eligible").Default(false).Immutable(),
		field.Bool("quota_grace_eligible").Default(false),
		field.Enum("status").Values("active", "request_scoped").Immutable(),
		field.String("selected_logical_model").MaxLen(100).Optional().Nillable(),
		field.Enum("selected_tier").Values("economy", "general", "advanced").Optional().Nillable(),
		field.Enum("selected_complexity").Values("simple", "general", "complex").Optional().Nillable(),
		field.JSON("required_capabilities", []string{}).Default([]string{}),
		field.Int64("routing_version").Default(0),
		field.UUID("first_gateway_request_id", uuid.UUID{}).Immutable(),
		field.UUID("last_gateway_request_id", uuid.UUID{}),
		field.Time("created_at").Default(time.Now).Immutable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("last_activity_at").Default(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (WorkSession) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tenant_id", "employee_user_id", "profile_version", "signal_source", "hmac_key_version", "session_key_hmac").
			Unique().StorageKey("work_sessions_reliable_identity_key").Annotations(entsql.IndexWhere("reliability = 'reliable'")),
		index.Fields("employee_user_id", "last_activity_at").StorageKey("work_sessions_employee_activity_idx"),
		index.Fields("config_version", "created_at").StorageKey("work_sessions_config_version_idx"),
	}
}
