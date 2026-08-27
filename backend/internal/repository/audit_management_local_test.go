//go:build auditFoundationmigration

package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestAuditManagementDisclosureAndRetentionAgainstPostgres(t *testing.T) {
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

	superID := seedAuditManagementSuperAdmin(t, ctx, db)
	adminID := seedAuditManagementUser(t, ctx, db, service.RoleAdmin)
	userID := seedAuditManagementUser(t, ctx, db, service.RoleUser)
	require.Positive(t, adminID)

	key := bytes.Repeat([]byte{0x7a}, 32)
	codec, err := service.NewAuditPartCodec(key, "auditManagement-v1")
	require.NoError(t, err)
	t.Setenv("AUDIT_MANAGEMENT_LOCAL_AUDIT_KEY", base64.StdEncoding.EncodeToString(key))
	foundationRepo := NewAuditFoundationRepository(db)
	managementRepo := NewAuditManagementRepository(db)
	foundation := service.NewAuditFoundationService(foundationRepo, config.AuditConfig{
		Mode: service.AuditModeRequired, ContentKeyRef: "env:AUDIT_MANAGEMENT_LOCAL_AUDIT_KEY",
		ContentKeyVersion: "auditManagement-v1", ReconcileIntervalSeconds: 3600,
	})
	foundation.Start()
	defer foundation.Stop()
	management := service.NewAuditManagementService(managementRepo, foundation)

	viewable := createAuditManagementFinalizedInteraction(t, ctx, db, foundationRepo, codec, userID, time.Now().UTC().Add(-time.Hour), "auditManagement-viewable")
	actor := service.AuditDisclosureActor{UserID: superID, SessionVersion: 0, SessionExpiresAt: time.Now().Add(time.Hour), Role: service.RoleSuperAdmin, AuthMethod: "jwt"}
	disclosed, err := management.Disclose(ctx, service.AuditDisclosureInput{
		InteractionID: viewable.ID, Actor: actor,
	})
	require.NoError(t, err)
	require.Len(t, disclosed.Parts, 1)
	require.Contains(t, disclosed.Parts[0].Content, "auditManagement-viewable")
	assertAuditManagementDisclosureEvents(t, ctx, db, disclosed.OperationID, "started", "succeeded")

	metadata, err := management.ListMetadata(ctx, service.AuditMetadataFilter{
		Employee: fmt.Sprintf("%d", userID), Protocol: "openai", Model: "auditManagement-model",
		RequestOutcome: service.AuditRequestCompleted, ContentState: service.AuditContentComplete,
		GatewayRequestID: &viewable.GatewayRequestID, Page: 1, PageSize: 20,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), metadata.Total)
	encodedMetadata, err := json.Marshal(metadata)
	require.NoError(t, err)
	for _, forbidden := range []string{"api_key_id", "api_key_fingerprint", "nonce", "ciphertext", "auth_tag", "key_version"} {
		require.NotContains(t, string(encodedMetadata), forbidden)
	}

	_, err = management.Disclose(ctx, service.AuditDisclosureInput{
		InteractionID: viewable.ID,
		Actor:         service.AuditDisclosureActor{UserID: adminID, SessionVersion: 0, SessionExpiresAt: time.Now().Add(time.Hour), Role: service.RoleAdmin, AuthMethod: "jwt"},
	})
	require.ErrorIs(t, err, service.ErrAuditDisclosureForbidden)
	_, err = management.Disclose(ctx, service.AuditDisclosureInput{
		InteractionID: viewable.ID,
		Actor:         service.AuditDisclosureActor{Role: service.RoleSuperAdmin, AuthMethod: "admin_api_key"},
	})
	require.ErrorIs(t, err, service.ErrAuditDisclosureForbidden)

	t.Run("revoked during disclosure fails before plaintext release", func(t *testing.T) {
		wrapped := &auditManagementRevokingManagementRepo{AuditManagementRepository: managementRepo, db: db, userID: superID}
		svc := service.NewAuditManagementService(wrapped, foundation)
		result, discloseErr := svc.Disclose(ctx, service.AuditDisclosureInput{
			InteractionID: viewable.ID, Actor: actor,
		})
		require.ErrorIs(t, discloseErr, service.ErrAuditDisclosureForbidden)
		require.Empty(t, result.Parts)
		assertAuditManagementDisclosureEvents(t, ctx, db, wrapped.operationID, "started", "failed")
		_, err := db.ExecContext(ctx, `UPDATE users SET session_version=0 WHERE id=$1`, superID)
		require.NoError(t, err)
	})

	t.Run("governance completion failure returns no plaintext", func(t *testing.T) {
		_, err := db.ExecContext(ctx, `
CREATE OR REPLACE FUNCTION auditManagement_reject_completion() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.action='raw_content_disclosure' AND NEW.event_sequence=2 THEN
    RAISE EXCEPTION 'synthetic auditManagement completion failure';
  END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER auditManagement_reject_completion BEFORE INSERT ON governance_events
FOR EACH ROW EXECUTE FUNCTION auditManagement_reject_completion()`)
		require.NoError(t, err)
		result, discloseErr := management.Disclose(ctx, service.AuditDisclosureInput{
			InteractionID: viewable.ID, Actor: actor,
		})
		require.ErrorIs(t, discloseErr, service.ErrAuditGovernanceUnavailable)
		require.Empty(t, result.Parts)
		_, err = db.ExecContext(ctx, `DROP TRIGGER auditManagement_reject_completion ON governance_events; DROP FUNCTION auditManagement_reject_completion()`)
		require.NoError(t, err)
	})

	cutoff := time.Now().UTC().Truncate(time.Microsecond)
	before := createAuditManagementFinalizedInteraction(t, ctx, db, foundationRepo, codec, userID, cutoff.Add(-180*24*time.Hour-time.Second), "auditManagement-before-boundary")
	exact := createAuditManagementFinalizedInteraction(t, ctx, db, foundationRepo, codec, userID, cutoff.Add(-180*24*time.Hour), "auditManagement-exact-boundary")
	after := createAuditManagementFinalizedInteraction(t, ctx, db, foundationRepo, codec, userID, cutoff.Add(-180*24*time.Hour+time.Second), "auditManagement-after-boundary")
	usageID := seedAuditManagementUsage(t, ctx, db, userID, exact.GatewayRequestID)

	retention, err := management.RunRetention(ctx, cutoff)
	require.NoError(t, err)
	require.Equal(t, service.AuditRetentionResult{Candidates: 2, Purged: 2, Failed: 0}, retention)
	assertAuditManagementContentState(t, ctx, db, before.ID, service.AuditContentExpired, 0)
	assertAuditManagementContentState(t, ctx, db, exact.ID, service.AuditContentExpired, 0)
	assertAuditManagementContentState(t, ctx, db, after.ID, service.AuditContentComplete, 1)
	var usageCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_logs WHERE id=$1 AND gateway_request_id=$2`, usageID, exact.GatewayRequestID).Scan(&usageCount))
	require.Equal(t, 1, usageCount)

	repeated, err := management.RunRetention(ctx, cutoff)
	require.NoError(t, err)
	require.Zero(t, repeated.Candidates)
	require.Zero(t, repeated.Purged)

	t.Run("partial failure is isolated and retryable", func(t *testing.T) {
		first := createAuditManagementFinalizedInteraction(t, ctx, db, foundationRepo, codec, userID, cutoff.Add(-181*24*time.Hour), "auditManagement-retry-first")
		second := createAuditManagementFinalizedInteraction(t, ctx, db, foundationRepo, codec, userID, cutoff.Add(-181*24*time.Hour), "auditManagement-retry-second")
		_, err := db.ExecContext(ctx, fmt.Sprintf(`
CREATE OR REPLACE FUNCTION auditManagement_fail_one_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.interaction_id='%s'::uuid THEN
    RAISE EXCEPTION 'synthetic auditManagement per-interaction failure';
  END IF;
  RETURN OLD;
END $$;
CREATE TRIGGER auditManagement_fail_one_delete BEFORE DELETE ON audit_content_parts
FOR EACH ROW EXECUTE FUNCTION auditManagement_fail_one_delete()`, first.ID))
		require.NoError(t, err)
		partial, purgeErr := management.RunRetention(ctx, cutoff)
		require.NoError(t, purgeErr)
		require.Equal(t, 2, partial.Candidates)
		require.Equal(t, 1, partial.Purged)
		require.Equal(t, 1, partial.Failed)
		assertAuditManagementContentState(t, ctx, db, first.ID, service.AuditContentComplete, 1)
		assertAuditManagementContentState(t, ctx, db, second.ID, service.AuditContentExpired, 0)
		_, err = db.ExecContext(ctx, `DROP TRIGGER auditManagement_fail_one_delete ON audit_content_parts; DROP FUNCTION auditManagement_fail_one_delete()`)
		require.NoError(t, err)
		retry, retryErr := management.RunRetention(ctx, cutoff)
		require.NoError(t, retryErr)
		require.Equal(t, service.AuditRetentionResult{Candidates: 1, Purged: 1, Failed: 0}, retry)
		assertAuditManagementContentState(t, ctx, db, first.ID, service.AuditContentExpired, 0)
	})

	postCleanup, err := management.ListMetadata(ctx, service.AuditMetadataFilter{
		Employee: fmt.Sprintf("%d", userID), ContentState: service.AuditContentExpired, Page: 1, PageSize: 20,
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, postCleanup.Total, int64(4))
	for _, record := range postCleanup.Items {
		require.NotNil(t, record.SubjectUserID)
		require.NotNil(t, record.SubjectEmailSnapshot)
		require.Equal(t, service.AuditRequestCompleted, record.RequestOutcome)
	}
}

type auditManagementRevokingManagementRepo struct {
	service.AuditManagementRepository
	db          *sql.DB
	userID      int64
	operationID uuid.UUID
}

func (r *auditManagementRevokingManagementRepo) RecordDisclosureStarted(ctx context.Context, operationID uuid.UUID, actor service.AuditDisclosureActor, interactionID uuid.UUID) error {
	r.operationID = operationID
	return r.AuditManagementRepository.RecordDisclosureStarted(ctx, operationID, actor, interactionID)
}

func (r *auditManagementRevokingManagementRepo) LoadDisclosureMaterial(ctx context.Context, interactionID uuid.UUID) (service.AuditDisclosureMaterial, error) {
	material, err := r.AuditManagementRepository.LoadDisclosureMaterial(ctx, interactionID)
	if err != nil {
		return material, err
	}
	_, updateErr := r.db.ExecContext(ctx, `UPDATE users SET session_version=session_version+1 WHERE id=$1`, r.userID)
	return material, updateErr
}

func seedAuditManagementSuperAdmin(t *testing.T, ctx context.Context, db *sql.DB) int64 {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	var id int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO users(email,password_hash,role,balance,concurrency,status,session_version,created_at,updated_at)
VALUES($1,'synthetic','super_admin',0,1,'active',0,NOW(),NOW()) RETURNING id`, "auditManagement-super-"+uuid.NewString()+"@example.invalid").Scan(&id))
	_, err = tx.ExecContext(ctx, `INSERT INTO super_admin_seat(singleton_id,user_id,version,updated_at) VALUES(1,$1,1,NOW())`, id)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	return id
}

func seedAuditManagementUser(t *testing.T, ctx context.Context, db *sql.DB, role string) int64 {
	t.Helper()
	var id int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO users(email,password_hash,role,balance,concurrency,status,session_version,created_at,updated_at)
VALUES($1,'synthetic',$2,0,1,'active',0,NOW(),NOW()) RETURNING id`, "auditManagement-"+role+"-"+uuid.NewString()+"@example.invalid", role).Scan(&id))
	return id
}

func createAuditManagementFinalizedInteraction(t *testing.T, ctx context.Context, db *sql.DB, repo service.AuditFoundationRepository, codec *service.AuditPartCodec, userID int64, admittedAt time.Time, marker string) service.AuditInteractionRecord {
	t.Helper()
	admittedAt = admittedAt.UTC().Truncate(time.Microsecond)
	interactionID, gatewayID := uuid.New(), uuid.New()
	email := "auditManagement-employee@example.invalid"
	model := "auditManagement-model"
	plaintext := []byte(`{"version":"core-gateway-request-v1","body":"` + marker + `"}`)
	encrypted, err := codec.Encrypt(service.AuditPartAAD{
		InteractionID: interactionID, GatewayRequestID: gatewayID,
		Direction: "request", Sequence: 0, AdmittedAt: admittedAt, KeyVersion: "auditManagement-v1",
	}, plaintext)
	require.NoError(t, err)
	record := service.AuditInteractionRecord{
		ID: interactionID, GatewayRequestID: gatewayID, SubjectUserID: &userID,
		SubjectEmailSnapshot: &email, ProfileVersion: service.ProtocolProfileOpenAIResponsesV1,
		Protocol: "openai", Endpoint: "/v1/responses", Method: "POST", Transport: "http",
		RequestedModel: &model, AdmittedAt: admittedAt, ExpiresAt: admittedAt.Add(180 * 24 * time.Hour),
		LastActivityAt: admittedAt, RequestSHA256: encrypted.PlaintextSHA256, RequestPartCount: 1,
	}
	require.NoError(t, repo.AdmitRequest(ctx, record, service.AuditContentPartRecord{
		ID: uuid.New(), InteractionID: interactionID, Direction: "request", Sequence: 0,
		Encrypted: encrypted, DownstreamWriteResult: "not_applicable", CreatedAt: admittedAt,
	}))
	finishedAt := admittedAt.Add(500 * time.Millisecond)
	require.NoError(t, repo.FinalizeInteraction(ctx, service.AuditInteractionFinalization{
		InteractionID: interactionID, RequestOutcome: service.AuditRequestCompleted,
		ContentState: service.AuditContentComplete, WriteResult: "succeeded", At: finishedAt,
	}))
	_, err = db.ExecContext(ctx, `UPDATE audit_interactions SET resolved_model=$2 WHERE id=$1`, interactionID, model)
	require.NoError(t, err)
	return record
}

func assertAuditManagementDisclosureEvents(t *testing.T, ctx context.Context, db *sql.DB, operationID uuid.UUID, want ...string) {
	t.Helper()
	rows, err := db.QueryContext(ctx, `SELECT result::text,reason IS NULL FROM governance_events WHERE operation_id=$1 ORDER BY event_sequence`, operationID)
	require.NoError(t, err)
	defer rows.Close()
	got := make([]string, 0, len(want))
	for rows.Next() {
		var result string
		var reasonIsNull bool
		require.NoError(t, rows.Scan(&result, &reasonIsNull))
		require.True(t, reasonIsNull, "direct disclosure governance events must not invent a reason")
		got = append(got, result)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, want, got)
}

func assertAuditManagementContentState(t *testing.T, ctx context.Context, db *sql.DB, interactionID uuid.UUID, wantState string, wantParts int) {
	t.Helper()
	var state string
	var parts int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT content_state FROM audit_interactions WHERE id=$1`, interactionID).Scan(&state))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_content_parts WHERE interaction_id=$1`, interactionID).Scan(&parts))
	require.Equal(t, wantState, state)
	require.Equal(t, wantParts, parts)
}

func seedAuditManagementUsage(t *testing.T, ctx context.Context, db *sql.DB, userID int64, gatewayID uuid.UUID) int64 {
	t.Helper()
	var accountID, apiKeyID, usageID int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO accounts(name,platform,type,credentials,extra,concurrency,priority,status,created_at,updated_at)
VALUES($1,'openai','apikey','{}','{}',1,50,'active',NOW(),NOW()) RETURNING id`, "auditManagement-account-"+uuid.NewString()).Scan(&accountID))
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO api_keys(user_id,key,name,status,created_at,updated_at)
VALUES($1,$2,'auditManagement-key','active',NOW(),NOW()) RETURNING id`, userID, "sk-auditManagement-"+uuid.NewString()).Scan(&apiKeyID))
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO usage_logs(user_id,api_key_id,account_id,model,input_tokens,output_tokens,total_cost,actual_cost,gateway_request_id,created_at)
VALUES($1,$2,$3,'auditManagement-model',1,1,0,0,$4,NOW()) RETURNING id`, userID, apiKeyID, accountID, gatewayID).Scan(&usageID))
	return usageID
}
