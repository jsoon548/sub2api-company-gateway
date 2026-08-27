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

// AuditContentPart stores ciphertext and authenticated metadata only. It has
// no plaintext payload column and is not connected to a Gateway path in audit foundation.
type AuditContentPart struct{ ent.Schema }

func (AuditContentPart) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "audit_content_parts"}}
}

func (AuditContentPart) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(uuid.New).Immutable(),
		field.UUID("interaction_id", uuid.UUID{}).Immutable(),
		field.Enum("direction").Values("request", "response").Immutable(),
		field.Int("sequence").Immutable(),
		field.Bytes("nonce").Immutable(),
		field.Bytes("ciphertext").Immutable(),
		field.Bytes("auth_tag").Immutable(),
		field.String("key_version").MaxLen(64).Immutable(),
		field.String("aad_format_version").MaxLen(64).Immutable(),
		field.Int64("plaintext_length").Immutable(),
		field.Int64("ciphertext_length").Immutable(),
		field.Bytes("plaintext_sha256").Immutable(),
		field.Bytes("ciphertext_sha256").Immutable(),
		field.Enum("downstream_write_result").Values("not_applicable", "pending", "succeeded", "failed", "unknown").Default("not_applicable"),
		field.Time("created_at").Default(time.Now).Immutable().SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (AuditContentPart) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("interaction", AuditInteraction.Type).
			Ref("content_parts").
			Field("interaction_id").
			Required().
			Unique().
			Immutable().
			Annotations(entsql.OnDelete(entsql.Restrict)),
	}
}

func (AuditContentPart) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("interaction_id", "direction", "sequence").Unique().StorageKey("audit_content_parts_interaction_direction_sequence_key"),
		index.Fields("interaction_id", "created_at").StorageKey("audit_content_parts_interaction_created_idx"),
	}
}
