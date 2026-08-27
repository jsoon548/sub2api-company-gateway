package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// SuperAdminSeat stores the only live Super Administrator seat.
type SuperAdminSeat struct{ ent.Schema }

func (SuperAdminSeat) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{
		Table: "super_admin_seat",
		Checks: map[string]string{
			"super_admin_seat_singleton_check": "singleton_id = 1",
			"super_admin_seat_version_check":   "version > 0",
		},
	}}
}

func (SuperAdminSeat) Fields() []ent.Field {
	incremental := false
	return []ent.Field{
		field.Int16("id").StorageKey("singleton_id").Immutable().Annotations(entsql.Annotation{Incremental: &incremental}),
		field.Int64("user_id"),
		field.Int64("version").Default(1),
		field.Time("updated_at").SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (SuperAdminSeat) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("user", User.Type).
			Field("user_id").
			Unique().
			Required().
			Annotations(entsql.OnDelete(entsql.Restrict)),
	}
}

func (SuperAdminSeat) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id").Unique()}
}
