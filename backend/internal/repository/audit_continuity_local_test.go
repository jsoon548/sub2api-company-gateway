//go:build auditFoundationmigration

package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestAuditContinuityResponsePartTransactionAndFinalizationAgainstPostgres(t *testing.T) {
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
	codec, err := service.NewAuditPartCodec(bytes.Repeat([]byte{0x53}, 32), "v1")
	require.NoError(t, err)

	requestPlaintext := []byte(`{"version":"core-gateway-request-v1","body":"auditContinuity-request"}`)
	requestEncrypted, err := codec.Encrypt(service.AuditPartAAD{
		InteractionID: interactionID, GatewayRequestID: gatewayRequestID,
		Direction: "request", Sequence: 0, AdmittedAt: now, KeyVersion: "v1",
	}, requestPlaintext)
	require.NoError(t, err)
	interaction := service.AuditInteractionRecord{
		ID: interactionID, GatewayRequestID: gatewayRequestID,
		ProfileVersion: service.ProtocolProfileOpenAIResponsesV1, Protocol: "openai",
		Endpoint: "/v1/responses", Method: "POST", Transport: "sse",
		AdmittedAt: now, ExpiresAt: now.Add(180 * 24 * time.Hour), LastActivityAt: now,
		RequestSHA256: requestEncrypted.PlaintextSHA256, RequestPartCount: 1,
	}
	requestPart := service.AuditContentPartRecord{
		ID: uuid.New(), InteractionID: interactionID, Direction: "request", Sequence: 0,
		Encrypted: requestEncrypted, DownstreamWriteResult: "not_applicable", CreatedAt: now,
	}
	require.NoError(t, repo.AdmitRequest(ctx, interaction, requestPart))

	body := []byte("event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n")
	responseEnvelope := append([]byte(`{"version":"core-gateway-response-v1","status":200,"headers":[],"body":`), []byte(`"synthetic-base64"}`)...)
	responseEncrypted, err := codec.Encrypt(service.AuditPartAAD{
		InteractionID: interactionID, GatewayRequestID: gatewayRequestID,
		Direction: "response", Sequence: 0, AdmittedAt: now, KeyVersion: "v1",
	}, responseEnvelope)
	require.NoError(t, err)
	digest := sha256.Sum256(body)
	partID := uuid.New()
	commit := service.AuditResponsePartCommit{
		Part: service.AuditContentPartRecord{
			ID: partID, InteractionID: interactionID, Direction: "response", Sequence: 0,
			Encrypted: responseEncrypted, DownstreamWriteResult: "pending", CreatedAt: now.Add(time.Second),
		},
		ExpectedPartCount: 0, ResponseSHA256: digest[:], DownstreamStatus: http.StatusOK,
		At: now.Add(time.Second),
	}
	require.NoError(t, repo.CommitResponsePart(ctx, commit))

	var count, status int
	var storedDigest []byte
	var writeResult, requestOutcome, contentState string
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT response_part_count, response_sha256, downstream_status,
       downstream_write_result, request_outcome, content_state
FROM audit_interactions WHERE id=$1`, interactionID).Scan(
		&count, &storedDigest, &status, &writeResult, &requestOutcome, &contentState))
	require.Equal(t, 1, count)
	require.Equal(t, digest[:], storedDigest)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "pending", writeResult)
	require.Equal(t, service.AuditRequestProcessing, requestOutcome)
	require.Equal(t, service.AuditContentRecording, contentState)

	badCommit := commit
	badCommit.Part.ID = uuid.New()
	badCommit.Part.Sequence = 1
	badCommit.ExpectedPartCount = 99
	badCommit.Part.Encrypted, err = codec.Encrypt(service.AuditPartAAD{
		InteractionID: interactionID, GatewayRequestID: gatewayRequestID,
		Direction: "response", Sequence: 1, AdmittedAt: now, KeyVersion: "v1",
	}, []byte("must-roll-back"))
	require.NoError(t, err)
	require.ErrorIs(t, repo.CommitResponsePart(ctx, badCommit), service.ErrAuditCASConflict)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_content_parts WHERE interaction_id=$1 AND direction='response'`, interactionID).Scan(&count))
	require.Equal(t, 1, count, "failed metadata CAS must roll back the inserted response part")

	require.NoError(t, repo.SetResponseWriteResult(ctx, service.AuditResponseWriteResult{
		InteractionID: interactionID, PartID: partID, Sequence: 0, Result: "succeeded", At: now.Add(2 * time.Second),
	}))
	require.NoError(t, repo.FinalizeInteraction(ctx, service.AuditInteractionFinalization{
		InteractionID: interactionID, RequestOutcome: service.AuditRequestCompleted,
		ContentState: service.AuditContentComplete, WriteResult: "succeeded", At: now.Add(3 * time.Second),
	}))
	var requestVersion, contentVersion int64
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT request_outcome, content_state, request_outcome_version, content_state_version,
       downstream_write_result
FROM audit_interactions WHERE id=$1`, interactionID).Scan(
		&requestOutcome, &contentState, &requestVersion, &contentVersion, &writeResult))
	require.Equal(t, service.AuditRequestCompleted, requestOutcome)
	require.Equal(t, service.AuditContentComplete, contentState)
	require.Equal(t, int64(1), requestVersion)
	require.Equal(t, int64(1), contentVersion)
	require.Equal(t, "succeeded", writeResult)
}
