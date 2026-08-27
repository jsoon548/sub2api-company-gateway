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

type AutoCandidatePool struct{ ent.Schema }

func (AutoCandidatePool) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "auto_candidate_pools"}}
}

func (AutoCandidatePool) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.Int64("generation").Immutable(),
		field.Enum("tier").Values("economy", "general", "advanced").Immutable(),
		field.Int("position").Positive().Immutable(),
		field.UUID("catalog_entry_id", uuid.UUID{}).Immutable(),
		field.Time("valid_from").Immutable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("valid_until").Optional().Nillable().Immutable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("created_at").Default(time.Now).Immutable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (AutoCandidatePool) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("generation", "tier", "position").Unique().StorageKey("auto_candidate_pool_position_key"),
		index.Fields("generation", "tier", "catalog_entry_id").Unique().StorageKey("auto_candidate_pool_entry_key"),
	}
}
