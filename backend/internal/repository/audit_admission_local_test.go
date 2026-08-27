//go:build auditFoundationmigration

package repository

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestAuditAdmissionTransactionAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("AUDIT_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Fatal("AUDIT_TEST_DATABASE_DSN is required")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	ctx := context.Background()
	resetAuditFoundationSchema(t, ctx, db)
	require.NoError(t, ApplyMigrations(ctx, db))
	repo := NewAuditFoundationRepository(db)
	now := time.Now().UTC().Truncate(time.Microsecond)
	interactionID, gatewayRequestID := uuid.New(), uuid.New()
	codec, err := service.NewAuditPartCodec(bytes.Repeat([]byte{0x42}, 32), "v1")
	require.NoError(t, err)
	plaintext := []byte(`{"version":"core-gateway-request-v1","request_uri":"/v1/messages?tool=synthetic","body":"synthetic-request-only"}`)
	encrypted, err := codec.Encrypt(service.AuditPartAAD{
		InteractionID: interactionID, GatewayRequestID: gatewayRequestID,
		Direction: "request", Sequence: 0, AdmittedAt: now, KeyVersion: "v1",
	}, plaintext)
	require.NoError(t, err)
	interaction := service.AuditInteractionRecord{ID: interactionID, GatewayRequestID: gatewayRequestID, ProfileVersion: service.ProtocolProfileAnthropicMessagesV1, Protocol: "anthropic", Endpoint: "/v1/messages", Method: "POST", Transport: "http", AdmittedAt: now, ExpiresAt: now.Add(180 * 24 * time.Hour), LastActivityAt: now, RequestSHA256: encrypted.PlaintextSHA256, RequestPartCount: 1}
	part := service.AuditContentPartRecord{ID: uuid.New(), InteractionID: interaction.ID, Direction: "request", Sequence: 0, Encrypted: encrypted, DownstreamWriteResult: "not_applicable", CreatedAt: now}
	require.NoError(t, repo.AdmitRequest(ctx, interaction, part))
	var interactions, parts, requestPartCount int
	var requestOutcome, contentState string
	var requestOutcomeVersion, contentStateVersion int64
	var responsePartCount int
	var storedHash []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_interactions WHERE id=$1`, interaction.ID).Scan(&interactions))
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT request_part_count, response_part_count, request_outcome, content_state,
       request_outcome_version, content_state_version, request_sha256
FROM audit_interactions WHERE id=$1`, interaction.ID).Scan(
		&requestPartCount, &responsePartCount, &requestOutcome, &contentState,
		&requestOutcomeVersion, &contentStateVersion, &storedHash,
	))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_content_parts WHERE interaction_id=$1`, interaction.ID).Scan(&parts))
	require.Equal(t, 1, interactions)
	require.Equal(t, 1, parts)
	require.Equal(t, 1, requestPartCount)
	require.Zero(t, responsePartCount, "audit admission admission must not create response parts")
	require.Equal(t, service.AuditRequestProcessing, requestOutcome)
	require.Equal(t, service.AuditContentRecording, contentState)
	require.Zero(t, requestOutcomeVersion)
	require.Zero(t, contentStateVersion)
	require.Equal(t, encrypted.PlaintextSHA256, storedHash)
	var storedCiphertext []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT ciphertext FROM audit_content_parts WHERE interaction_id=$1`, interaction.ID).Scan(&storedCiphertext))
	require.NotContains(t, string(storedCiphertext), "synthetic-request-only")

	failed := interaction
	failed.ID, failed.GatewayRequestID = uuid.New(), uuid.New()
	badPart := part
	badPart.ID, badPart.InteractionID = uuid.New(), failed.ID
	badPart.Encrypted.Nonce = []byte{1}
	require.Error(t, repo.AdmitRequest(ctx, failed, badPart))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_interactions WHERE id=$1`, failed.ID).Scan(&interactions))
	require.Zero(t, interactions, "failed content insert must roll back the interaction")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_content_parts WHERE interaction_id=$1`, failed.ID).Scan(&parts))
	require.Zero(t, parts, "failed content insert must leave no orphaned request part")
}
