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

type ModelCatalogEntry struct{ ent.Schema }

func (ModelCatalogEntry) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "model_catalog_entries"}}
}

func (ModelCatalogEntry) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.Int64("generation").Immutable(),
		field.String("logical_model").MaxLen(100).Immutable(),
		field.String("provider_model").MaxLen(100).Immutable(),
		field.Enum("tier").Values("economy", "general", "advanced").Immutable(),
		field.JSON("capabilities", []string{}).Default([]string{}).Immutable(),
		field.Time("valid_from").Immutable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("valid_until").Optional().Nillable().Immutable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Bool("emergency_disabled").Default(false),
		field.Time("created_at").Default(time.Now).Immutable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now).SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (ModelCatalogEntry) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("generation", "logical_model").Unique().StorageKey("model_catalog_generation_model_key"),
		index.Fields("generation", "tier", "logical_model").StorageKey("model_catalog_generation_tier_idx"),
		index.Fields("logical_model").StorageKey("model_catalog_emergency_idx").Annotations(entsql.IndexWhere("emergency_disabled")),
	}
}
