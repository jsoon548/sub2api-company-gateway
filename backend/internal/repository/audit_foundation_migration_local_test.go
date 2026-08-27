//go:build auditFoundationmigration

package repository

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	migrationassets "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const auditFoundationMigrationName = "174_core_audit_foundation.sql"

func TestAuditFoundationMigrationAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("AUDIT_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Fatal("AUDIT_TEST_DATABASE_DSN is required")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	ctx := context.Background()
	require.NoError(t, db.PingContext(ctx))

	beforeAuditFoundation, auditFoundationOnly := splitAuditFoundationMigrationFS(t)

	// Fresh database: apply 001 through 174 and repeat without drift.
	resetAuditFoundationSchema(t, ctx, db)
	require.NoError(t, applyMigrationsFS(ctx, db, mergeAuditFoundationMigrationFS(beforeAuditFoundation, auditFoundationOnly)))
	assertAuditFoundationMigrationBoundary(t, ctx, db, 174)
	assertAuditFoundationSchema(t, ctx, db)
	require.NoError(t, applyMigrationsFS(ctx, db, mergeAuditFoundationMigrationFS(beforeAuditFoundation, auditFoundationOnly)))
	var applied174 int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE filename=$1`, auditFoundationMigrationName).Scan(&applied174))
	require.Equal(t, 1, applied174)

	// Real 173 -> 174 upgrade.
	resetAuditFoundationSchema(t, ctx, db)
	require.NoError(t, applyMigrationsFS(ctx, db, beforeAuditFoundation))
	assertAuditFoundationMigrationBoundary(t, ctx, db, 173)
	var absentBeforeUpgrade bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('public.audit_interactions') IS NULL`).Scan(&absentBeforeUpgrade))
	require.True(t, absentBeforeUpgrade)
	require.NoError(t, applyMigrationsFS(ctx, db, auditFoundationOnly))
	assertAuditFoundationMigrationBoundary(t, ctx, db, 174)
	assertAuditFoundationSchema(t, ctx, db)

	testAuditFoundationCodecPersistenceAndConstraints(t, ctx, db)
	testAuditFoundationCASAndTerminalGuards(t, ctx, db)
	testAuditFoundationReconciliationRestart(t, ctx, db)

	// A failure after the 174 statements must roll back every audit foundation object and
	// the schema_migrations row.
	resetAuditFoundationSchema(t, ctx, db)
	require.NoError(t, applyMigrationsFS(ctx, db, beforeAuditFoundation))
	broken := cloneAuditFoundationMigrationFS(t, auditFoundationOnly)
	brokenFile := broken[auditFoundationMigrationName]
	brokenFile.Data = append(append([]byte(nil), brokenFile.Data...), []byte("\nSELECT auditFoundation_force_transaction_rollback();\n")...)
	require.Error(t, applyMigrationsFS(ctx, db, broken))
	var tablesAfterRollback, columnAfterRollback, rowAfterRollback int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('audit_interactions','audit_content_parts')`).Scan(&tablesAfterRollback))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='usage_logs' AND column_name='gateway_request_id'`).Scan(&columnAfterRollback))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE filename=$1`, auditFoundationMigrationName).Scan(&rowAfterRollback))
	require.Zero(t, tablesAfterRollback)
	require.Zero(t, columnAfterRollback)
	require.Zero(t, rowAfterRollback)
}

func splitAuditFoundationMigrationFS(t *testing.T) (fstest.MapFS, fstest.MapFS) {
	t.Helper()
	names, err := fs.Glob(migrationassets.FS, "*.sql")
	require.NoError(t, err)
	before := fstest.MapFS{}
	auditFoundation := fstest.MapFS{}
	for _, name := range names {
		data, readErr := fs.ReadFile(migrationassets.FS, name)
		require.NoError(t, readErr)
		entry := &fstest.MapFile{Data: data, Mode: 0o444}
		switch {
		case name < auditFoundationMigrationName:
			before[name] = entry
		case name == auditFoundationMigrationName:
			auditFoundation[name] = entry
		default:
			// Later task migrations are intentionally excluded from this scoped
			// audit foundation fixture. Their boundaries are verified by their own tests.
		}
	}
	require.Contains(t, auditFoundation, auditFoundationMigrationName)
	return before, auditFoundation
}

func mergeAuditFoundationMigrationFS(parts ...fstest.MapFS) fstest.MapFS {
	merged := fstest.MapFS{}
	for _, part := range parts {
		for name, file := range part {
			merged[name] = file
		}
	}
	return merged
}

func cloneAuditFoundationMigrationFS(t *testing.T, source fstest.MapFS) fstest.MapFS {
	t.Helper()
	cloned := fstest.MapFS{}
	for name, file := range source {
		cloned[name] = &fstest.MapFile{Data: append([]byte(nil), file.Data...), Mode: file.Mode}
	}
	return cloned
}

func resetAuditFoundationSchema(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public`)
	require.NoError(t, err)
}

func assertAuditFoundationMigrationBoundary(t *testing.T, ctx context.Context, db *sql.DB, want int) {
	t.Helper()
	var filename string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT filename FROM schema_migrations ORDER BY filename DESC LIMIT 1`).Scan(&filename))
	if want == 173 {
		require.True(t, strings.HasPrefix(filename, "173_"), filename)
	} else {
		require.True(t, strings.HasPrefix(filename, "174_"), filename)
	}
	var later int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE filename >= '175_'`).Scan(&later))
	require.Zero(t, later)
}

func assertAuditFoundationSchema(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var auditTables int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='public' AND table_name IN ('audit_interactions','audit_content_parts')`).Scan(&auditTables))
	require.Equal(t, 2, auditTables)
	for _, forbidden := range []string{"audit_disclosure_events", "audit_export_jobs"} {
		var absent bool
		require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('public.' || $1) IS NULL`, forbidden).Scan(&absent))
		require.True(t, absent, forbidden)
	}

	var dataType, nullable string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT data_type,is_nullable FROM information_schema.columns WHERE table_schema='public' AND table_name='usage_logs' AND column_name='gateway_request_id'`).Scan(&dataType, &nullable))
	require.Equal(t, "uuid", dataType)
	require.Equal(t, "YES", nullable)

	for _, indexName := range []string{
		"audit_interactions_gateway_request_id_key",
		"audit_interactions_subject_admitted_idx",
		"audit_interactions_outcome_state_activity_idx",
		"audit_interactions_expires_idx",
		"audit_content_parts_interaction_direction_sequence_key",
		"audit_content_parts_interaction_created_idx",
		"usage_logs_gateway_request_id_idx",
	} {
		var count int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pg_indexes WHERE schemaname='public' AND indexname=$1`, indexName).Scan(&count))
		require.Equal(t, 1, count, indexName)
	}
}

func testAuditFoundationCodecPersistenceAndConstraints(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	repo := NewAuditFoundationRepository(db)
	interaction := createAuditFoundationInteraction(t, ctx, repo, time.Now().UTC().Add(-time.Minute))
	key := bytes.Repeat([]byte{0x42}, 32)
	codec, err := service.NewAuditPartCodec(key, "auditFoundation-v1")
	require.NoError(t, err)
	plaintext := []byte("AUDIT_SYNTHETIC_PLAINTEXT_9fd6c1")
	aad := service.AuditPartAAD{
		InteractionID:    interaction.ID,
		GatewayRequestID: interaction.GatewayRequestID,
		Direction:        "request",
		Sequence:         0,
		AdmittedAt:       interaction.AdmittedAt,
		KeyVersion:       "auditFoundation-v1",
	}
	encrypted, err := codec.Encrypt(aad, plaintext)
	require.NoError(t, err)
	require.NoError(t, repo.AppendEncryptedPart(ctx, service.AuditContentPartRecord{
		ID: uuid.New(), InteractionID: interaction.ID, Direction: "request", Sequence: 0,
		Encrypted: encrypted, CreatedAt: time.Now().UTC(),
	}))
	require.Error(t, repo.AppendEncryptedPart(ctx, service.AuditContentPartRecord{
		ID: uuid.New(), InteractionID: interaction.ID, Direction: "request", Sequence: 0,
		Encrypted: encrypted, CreatedAt: time.Now().UTC(),
	}))

	var plaintextMatches, keyMatches, rawKeyMatches int
	encodedKey := base64.StdEncoding.EncodeToString(key)
	hexKey := hex.EncodeToString(key)
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM audit_content_parts
WHERE encode(ciphertext, 'hex') LIKE '%' || encode(convert_to($1, 'UTF8'), 'hex') || '%'
   OR encode(auth_tag, 'hex') LIKE '%' || encode(convert_to($1, 'UTF8'), 'hex') || '%'`, string(plaintext)).Scan(&plaintextMatches))
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM audit_interactions
	WHERE COALESCE(safe_error_summary,'') LIKE '%' || $1 || '%'
   OR COALESCE(api_key_fingerprint,'') LIKE '%' || $1 || '%'`, encodedKey).Scan(&keyMatches))
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM audit_content_parts
WHERE encode(nonce, 'hex') LIKE '%' || $1 || '%'
   OR encode(ciphertext, 'hex') LIKE '%' || $1 || '%'
   OR encode(auth_tag, 'hex') LIKE '%' || $1 || '%'
   OR encode(plaintext_sha256, 'hex') LIKE '%' || $1 || '%'
   OR encode(ciphertext_sha256, 'hex') LIKE '%' || $1 || '%'`, hexKey).Scan(&rawKeyMatches))
	require.Zero(t, plaintextMatches)
	require.Zero(t, keyMatches)
	require.Zero(t, rawKeyMatches)

	bad := encrypted
	bad.Nonce = []byte{1, 2, 3}
	require.Error(t, repo.AppendEncryptedPart(ctx, service.AuditContentPartRecord{
		ID: uuid.New(), InteractionID: interaction.ID, Direction: "response", Sequence: 0,
		Encrypted: bad, CreatedAt: time.Now().UTC(),
	}))
}

func testAuditFoundationCASAndTerminalGuards(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	repo := NewAuditFoundationRepository(db)
	svc := service.NewAuditFoundationService(repo, config.AuditConfig{Mode: service.AuditModeDisabled})

	for _, terminal := range []string{service.AuditRequestRejectedPreUpstream, service.AuditRequestCompleted, service.AuditRequestUpstreamFailed, service.AuditRequestInterrupted} {
		row := createAuditFoundationInteraction(t, ctx, repo, time.Now().UTC().Add(-time.Minute))
		require.NoError(t, svc.AdvanceRequestOutcome(ctx, service.AuditStateCAS{
			InteractionID: row.ID, ExpectedState: service.AuditRequestProcessing, ExpectedVersion: 0, NextState: terminal,
		}))
	}

	row := createAuditFoundationInteraction(t, ctx, repo, time.Now().UTC().Add(-time.Minute))
	changes := []service.AuditStateCAS{
		{InteractionID: row.ID, ExpectedState: service.AuditRequestProcessing, ExpectedVersion: 0, NextState: service.AuditRequestCompleted},
		{InteractionID: row.ID, ExpectedState: service.AuditRequestProcessing, ExpectedVersion: 0, NextState: service.AuditRequestInterrupted},
	}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range changes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = svc.AdvanceRequestOutcome(ctx, changes[i])
		}(i)
	}
	wg.Wait()
	var successes, conflicts int
	for _, err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, service.ErrAuditCASConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent CAS error: %v", err)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
	_, err := db.ExecContext(ctx, `UPDATE audit_interactions SET request_outcome='processing', request_outcome_version=request_outcome_version+1 WHERE id=$1`, row.ID)
	require.Error(t, err)

	contentComplete := createAuditFoundationInteraction(t, ctx, repo, time.Now().UTC().Add(-time.Minute))
	require.NoError(t, svc.AdvanceContentState(ctx, service.AuditStateCAS{
		InteractionID: contentComplete.ID, ExpectedState: service.AuditContentRecording, ExpectedVersion: 0, NextState: service.AuditContentComplete,
	}))
	require.NoError(t, svc.AdvanceContentState(ctx, service.AuditStateCAS{
		InteractionID: contentComplete.ID, ExpectedState: service.AuditContentComplete, ExpectedVersion: 1, NextState: service.AuditContentExpired,
	}))
	_, err = db.ExecContext(ctx, `UPDATE audit_interactions SET content_state='recording', content_state_version=content_state_version+1 WHERE id=$1`, contentComplete.ID)
	require.Error(t, err)

	contentIncomplete := createAuditFoundationInteraction(t, ctx, repo, time.Now().UTC().Add(-time.Minute))
	require.NoError(t, svc.AdvanceContentState(ctx, service.AuditStateCAS{
		InteractionID: contentIncomplete.ID, ExpectedState: service.AuditContentRecording, ExpectedVersion: 0, NextState: service.AuditContentIncomplete,
	}))
	require.NoError(t, svc.AdvanceContentState(ctx, service.AuditStateCAS{
		InteractionID: contentIncomplete.ID, ExpectedState: service.AuditContentIncomplete, ExpectedVersion: 1, NextState: service.AuditContentExpired,
	}))
}

func testAuditFoundationReconciliationRestart(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	repo := NewAuditFoundationRepository(db)
	row := createAuditFoundationInteraction(t, ctx, repo, time.Now().UTC().Add(-time.Hour))
	keyEnv := "AUDIT_AUDIT_RECONCILIATION_CONTENT_KEY"
	t.Setenv(keyEnv, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x7c}, 32)))
	cfg := config.AuditConfig{
		Mode: service.AuditModeRequired, ContentKeyRef: "env:" + keyEnv, ContentKeyVersion: "auditFoundation-v1",
		ReconcileStaleAfterSeconds: 1, ReconcileIntervalSeconds: 3600,
	}
	first := service.NewAuditFoundationService(repo, cfg)
	first.Start()
	require.True(t, first.Status().FoundationReady)
	first.Stop()

	var outcome, content string
	var outcomeVersion, contentVersion int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT request_outcome,content_state,request_outcome_version,content_state_version FROM audit_interactions WHERE id=$1`, row.ID).Scan(&outcome, &content, &outcomeVersion, &contentVersion))
	require.Equal(t, service.AuditRequestInterrupted, outcome)
	require.Equal(t, service.AuditContentIncomplete, content)
	require.Equal(t, int64(1), outcomeVersion)
	require.Equal(t, int64(1), contentVersion)

	second := service.NewAuditFoundationService(repo, cfg)
	second.Start()
	require.True(t, second.Status().FoundationReady)
	second.Stop()
	var afterOutcomeVersion, afterContentVersion int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT request_outcome_version,content_state_version FROM audit_interactions WHERE id=$1`, row.ID).Scan(&afterOutcomeVersion, &afterContentVersion))
	require.Equal(t, outcomeVersion, afterOutcomeVersion)
	require.Equal(t, contentVersion, afterContentVersion)
}

func createAuditFoundationInteraction(t *testing.T, ctx context.Context, repo service.AuditFoundationRepository, admittedAt time.Time) service.AuditInteractionRecord {
	t.Helper()
	record := service.AuditInteractionRecord{
		ID: uuid.New(), GatewayRequestID: uuid.New(), ProfileVersion: "auditFoundation-foundation-v1",
		Protocol: "openai", Endpoint: "/v1/responses", Method: "POST", Transport: "http",
		AdmittedAt: admittedAt, ExpiresAt: admittedAt.Add(180 * 24 * time.Hour), LastActivityAt: admittedAt,
	}
	require.NoError(t, repo.CreateInteraction(ctx, record))
	return record
}
