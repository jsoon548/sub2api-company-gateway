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

func TestWorkSessionMigrationAndWorkSessionRepositoryAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("AUDIT_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Fatal("AUDIT_TEST_DATABASE_DSN is required")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	ctx := context.Background()
	require.NoError(t, db.PingContext(ctx))
	resetAuditFoundationSchema(t, ctx, db)
	require.NoError(t, ApplyMigrations(ctx, db))
	require.NoError(t, ApplyMigrations(ctx, db), "Migration 175 must be idempotently recorded")

	var lastMigration string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT filename FROM schema_migrations ORDER BY filename DESC LIMIT 1`).Scan(&lastMigration))
	require.Equal(t, "175_work_session_auto.sql", lastMigration)
	var later int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE filename >= '176_'`).Scan(&later))
	require.Zero(t, later)
	for _, table := range []string{"work_sessions", "model_catalog_entries", "auto_candidate_pools"} {
		var exists bool
		require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, table).Scan(&exists))
		require.True(t, exists, table)
	}

	seedAuditManagementSuperAdmin(t, ctx, db)
	userID := seedAuditManagementUser(t, ctx, db, service.RoleUser)
	repo := NewWorkSessionRepository(db)
	require.NoError(t, repo.CheckFoundation(ctx))
	now := time.Now().UTC().Truncate(time.Microsecond)

	firstGeneration, err := repo.ReplaceManagementConfig(ctx, service.WorkSessionManagementUpdate{
		Catalog: []service.ModelCatalogInput{
			{LogicalModel: "shared-model", ProviderModel: "provider-shared-v1", Tier: service.ModelTierGeneral, Capabilities: []string{"tools"}, ValidFrom: &now},
			{LogicalModel: "old-only", ProviderModel: "provider-old", Tier: service.ModelTierEconomy, Capabilities: []string{"code"}, ValidFrom: &now},
		},
		CandidatePools: []service.AutoCandidatePoolInput{
			{Tier: service.ModelTierEconomy, Candidates: []string{"old-only"}, ValidFrom: &now},
			{Tier: service.ModelTierGeneral, Candidates: []string{"shared-model"}, ValidFrom: &now},
		},
	}, now)
	require.NoError(t, err)
	require.Equal(t, int64(2), firstGeneration)

	rawSignal := "WORK_SESSION_RAW_SESSION_VALUE_MUST_NOT_PERSIST_9f6d"
	first := service.WorkSessionCreate{
		ID: uuid.New(), TenantID: "tenant-test", EmployeeUserID: userID,
		ProfileVersion: service.ProtocolProfileOpenAIResponsesV1,
		SignalSource:   service.WorkSessionSignalCodex, SignalStatus: service.WorkSessionSignalVerified,
		SessionKeyHMAC: bytes.Repeat([]byte{0x31}, 32), HMACKeyVersion: "workSession-v1",
		Reliability: service.WorkSessionReliabilityReliable, RoutingMode: service.WorkSessionRoutingExplicit,
		ConfigVersion: firstGeneration, GatewayRequestID: uuid.New(), At: now,
	}
	oldSession, err := repo.FindOrCreateReliable(ctx, first)
	require.NoError(t, err)
	first.ID, first.GatewayRequestID = uuid.New(), uuid.New()
	sameSession, err := repo.FindOrCreateReliable(ctx, first)
	require.NoError(t, err)
	require.Equal(t, oldSession.ID, sameSession.ID)

	unreliableInput := first
	unreliableInput.ID, unreliableInput.GatewayRequestID = uuid.New(), uuid.New()
	unreliableInput.SignalStatus = service.WorkSessionSignalMalformed
	unreliableInput.SessionKeyHMAC = nil
	unreliableInput.HMACKeyVersion = ""
	unreliableInput.Reliability = service.WorkSessionReliabilityUnreliable
	unreliable, err := repo.CreateUnreliable(ctx, unreliableInput)
	require.NoError(t, err)
	require.False(t, unreliable.AnalysisEligible)
	require.False(t, unreliable.QuotaGraceEligible)
	require.Nil(t, unreliable.HMACKeyVersion)

	secondGeneration, err := repo.ReplaceManagementConfig(ctx, service.WorkSessionManagementUpdate{
		Catalog: []service.ModelCatalogInput{
			{LogicalModel: "shared-model", ProviderModel: "provider-shared-v2", Tier: service.ModelTierGeneral, Capabilities: []string{"tools", "long_context"}, ValidFrom: &now},
			{LogicalModel: "new-only", ProviderModel: "provider-new", Tier: service.ModelTierAdvanced, Capabilities: []string{"reasoning"}, ValidFrom: &now},
		},
		CandidatePools: []service.AutoCandidatePoolInput{
			{Tier: service.ModelTierGeneral, Candidates: []string{"shared-model"}, ValidFrom: &now},
			{Tier: service.ModelTierAdvanced, Candidates: []string{"new-only"}, ValidFrom: &now},
		},
	}, now.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, int64(3), secondGeneration)

	newInput := first
	newInput.ID, newInput.GatewayRequestID = uuid.New(), uuid.New()
	newInput.SessionKeyHMAC = bytes.Repeat([]byte{0x32}, 32)
	newInput.ConfigVersion = secondGeneration
	newSession, err := repo.FindOrCreateReliable(ctx, newInput)
	require.NoError(t, err)

	available, err := repo.IsModelAvailableForSession(ctx, oldSession.ID, "old-only", now.Add(2*time.Second))
	require.NoError(t, err)
	require.True(t, available, "ordinary catalog changes must not rewrite existing sessions")
	available, err = repo.IsModelAvailableForSession(ctx, newSession.ID, "old-only", now.Add(2*time.Second))
	require.NoError(t, err)
	require.False(t, available, "new sessions must use the new catalog generation")

	require.NoError(t, repo.SetEmergencyDisabled(ctx, "shared-model", true, now.Add(3*time.Second)))
	for _, sessionID := range []uuid.UUID{oldSession.ID, newSession.ID} {
		available, err = repo.IsModelAvailableForSession(ctx, sessionID, "shared-model", now.Add(4*time.Second))
		require.NoError(t, err)
		require.False(t, available, "Emergency Model Disable must affect old and new sessions immediately")
	}
	postEmergencyGeneration, err := repo.ReplaceManagementConfig(ctx, service.WorkSessionManagementUpdate{
		Catalog:        []service.ModelCatalogInput{{LogicalModel: "shared-model", ProviderModel: "provider-shared-v3", Tier: service.ModelTierGeneral, Capabilities: []string{"tools"}, ValidFrom: &now}},
		CandidatePools: []service.AutoCandidatePoolInput{{Tier: service.ModelTierGeneral, Candidates: []string{"shared-model"}, ValidFrom: &now}},
	}, now.Add(5*time.Second))
	require.NoError(t, err)
	require.Equal(t, int64(4), postEmergencyGeneration)
	auto, catalog, _, _, err := repo.ListManagement(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, postEmergencyGeneration, auto.ConfigVersion)
	require.Len(t, catalog, 1)
	require.True(t, catalog[0].EmergencyDisabled, "ordinary catalog replacement must inherit the global emergency state")

	var plaintextMatches int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_sessions w WHERE to_jsonb(w)::text LIKE '%' || $1 || '%'`, rawSignal).Scan(&plaintextMatches))
	require.Zero(t, plaintextMatches)
	var reliableRows, unreliableRows, unreliableKeys int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FILTER (WHERE reliability='reliable'), COUNT(*) FILTER (WHERE reliability='unreliable'), COUNT(*) FILTER (WHERE reliability='unreliable' AND (session_key_hmac IS NOT NULL OR hmac_key_version IS NOT NULL)) FROM work_sessions`).Scan(&reliableRows, &unreliableRows, &unreliableKeys))
	require.Equal(t, 2, reliableRows)
	require.Equal(t, 1, unreliableRows)
	require.Zero(t, unreliableKeys)

	versions, err := repo.ListConfigVersions(ctx, postEmergencyGeneration)
	require.NoError(t, err)
	require.Len(t, versions, 3)
	require.Equal(t, postEmergencyGeneration, versions[0].ConfigVersion)
	require.True(t, versions[0].Current)
	require.Zero(t, versions[0].SessionCount)
	require.Equal(t, int64(1), versions[0].ModelCount)
	require.Equal(t, int64(1), versions[0].CandidateCount)
	require.Equal(t, secondGeneration, versions[1].ConfigVersion)
	require.Equal(t, int64(1), versions[1].SessionCount)
	require.Equal(t, firstGeneration, versions[2].ConfigVersion)
	require.Equal(t, int64(2), versions[2].SessionCount)
	require.Equal(t, int64(1), versions[2].ReliableSessionCount)
	require.Equal(t, int64(1), versions[2].RequestScopedSessionCount)
	require.Equal(t, "old-only", versions[2].Catalog[0].LogicalModel)
}
