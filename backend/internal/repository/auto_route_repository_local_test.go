//go:build auditFoundationmigration

package repository

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestAutoRoutingMigrationAndRouteDecisionRepositoryAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("AUDIT_TEST_DATABASE_DSN")
	if dsn == "" {
		t.Fatal("AUDIT_TEST_DATABASE_DSN is required")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer db.Close()
	ctx := context.Background()
	require.NoError(t, db.PingContext(ctx))

	// This test only initializes a newly-created, task-specific loopback database.
	// It never resets or reuses an existing schema.
	var databaseName, serverAddress string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT current_database(),inet_server_addr()::text`).Scan(&databaseName, &serverAddress))
	require.True(t, strings.HasPrefix(databaseName, "autoRouting_"), databaseName)
	require.Contains(t, []string{"127.0.0.1", "127.0.0.1/32", "::1", "::1/128"}, serverAddress)
	var existingTables int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='public'`).Scan(&existingTables))
	require.Zero(t, existingTables, "Auto routing repository test requires a fresh dedicated database")
	require.NoError(t, ApplyMigrations(ctx, db))

	var lastMigration string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT filename FROM schema_migrations ORDER BY filename DESC LIMIT 1`).Scan(&lastMigration))
	require.Equal(t, "175_work_session_auto.sql", lastMigration)
	for _, table := range []string{"route_decisions", "gateway_inference_runs"} {
		var exists bool
		require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, table).Scan(&exists))
		require.True(t, exists, table)
	}
	var oldUsageTable bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT to_regclass('public.gateway_analysis_usage') IS NOT NULL`).Scan(&oldUsageTable))
	require.False(t, oldUsageTable)
	var forbiddenRunColumns int
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM information_schema.columns
WHERE table_schema='public' AND table_name='gateway_inference_runs'
  AND column_name IN ('prompt','input','output','raw_response','credential','credential_ref')`).Scan(&forbiddenRunColumns))
	require.Zero(t, forbiddenRunColumns)
	var migration176 int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations WHERE filename >= '176_'`).Scan(&migration176))
	require.Zero(t, migration176)

	seedAuditManagementSuperAdmin(t, ctx, db)
	userID := seedAuditManagementUser(t, ctx, db, service.RoleUser)
	accountID, apiKeyID := seedGatewayUsageUsageDependencies(t, ctx, db, userID)
	now := time.Now().UTC().Truncate(time.Microsecond)
	repo := NewWorkSessionRepository(db).(*workSessionRepository)

	generation, err := repo.ReplaceManagementConfig(ctx, service.WorkSessionManagementUpdate{
		AutoEnabled: true, UserWhitelist: []int64{userID},
		Catalog: []service.ModelCatalogInput{
			{LogicalModel: "economy-model", ProviderModel: "provider-economy", Tier: service.ModelTierEconomy, ValidFrom: &now},
			{LogicalModel: "general-model", ProviderModel: "provider-general", Tier: service.ModelTierGeneral, Capabilities: []string{"image_input", "tool_use"}, ValidFrom: &now},
			{LogicalModel: "advanced-model", ProviderModel: "provider-advanced", Tier: service.ModelTierAdvanced, Capabilities: []string{"image_input", "tool_use", "long_context"}, ValidFrom: &now},
		},
		CandidatePools: []service.AutoCandidatePoolInput{
			{Tier: service.ModelTierEconomy, Candidates: []string{"economy-model"}, ValidFrom: &now},
			{Tier: service.ModelTierGeneral, Candidates: []string{"general-model"}, ValidFrom: &now},
			{Tier: service.ModelTierAdvanced, Candidates: []string{"advanced-model"}, ValidFrom: &now},
		},
	}, now)
	require.NoError(t, err)

	gatewayID := uuid.New()
	auditRepo := NewAuditFoundationRepository(db)
	email, requested := "autoRouting-user@example.invalid", "auto"
	require.NoError(t, auditRepo.CreateInteraction(ctx, service.AuditInteractionRecord{
		ID: uuid.New(), GatewayRequestID: gatewayID, SubjectUserID: &userID,
		SubjectEmailSnapshot: &email, ProfileVersion: service.ProtocolProfileOpenAIResponsesV1,
		Protocol: "openai", Endpoint: "/v1/responses", Method: "POST", Transport: "http",
		RequestedModel: &requested, AdmittedAt: now, ExpiresAt: now.Add(180 * 24 * time.Hour), LastActivityAt: now,
	}))
	session, err := repo.FindOrCreateReliable(ctx, service.WorkSessionCreate{
		ID: uuid.New(), TenantID: "autoRouting-tenant", EmployeeUserID: userID,
		ProfileVersion: service.ProtocolProfileOpenAIResponsesV1,
		SignalSource:   service.WorkSessionSignalCodex, SignalStatus: service.WorkSessionSignalVerified,
		SessionKeyHMAC: make([]byte, 32), HMACKeyVersion: "autoRouting-hmac-v1",
		Reliability: service.WorkSessionReliabilityReliable, RoutingMode: service.WorkSessionRoutingAuto,
		ConfigVersion: generation, GatewayRequestID: gatewayID, At: now,
	})
	require.NoError(t, err)
	require.NoError(t, repo.LinkGatewayRequest(ctx, gatewayID, session.ID))

	snapshot, err := repo.LoadAutoRoutingSnapshot(ctx, session.ID, now.Add(time.Second))
	require.NoError(t, err)
	require.Len(t, snapshot.Candidates, 3)
	require.Zero(t, snapshot.RoutingVersion)

	decisionID := uuid.New()
	actualModel, providerModel := "general-model", "provider-general"
	classifierVersion := service.AutoComplexityVersion
	classifierRunID := uuid.New()
	inputUnits, outputUnits := int64(12), int64(3)
	write := service.RouteDecisionWrite{
		ExpectedRoutingVersion: 0, SelectedCapabilities: []string{"tool_use"},
		Record: service.RouteDecisionRecord{
			ID: decisionID, GatewayRequestID: gatewayID, WorkSessionID: session.ID,
			EmployeeUserID: userID, ProfileVersion: service.ProtocolProfileOpenAIResponsesV1,
			ConfigVersion: generation, RequiredCapabilities: []string{"tool_use"},
			TaskComplexity: service.TaskComplexityGeneral, Certainty: service.DecisionCertaintyDecisive,
			Explanation: "Synthetic general classification.", DecisionSource: "classifier",
			RuleVersion: service.AutoRuleVersion, ClassifierRunID: &classifierRunID, ClassifierVersion: &classifierVersion,
			ClassifierStatus: service.ClassifierStatusCompleted, ClassifierLatencyMS: 17,
			RequestedTier: service.ModelTierGeneral, EffectiveTier: service.ModelTierGeneral,
			CandidatePool:      []service.RouteCandidateEvaluation{{Tier: service.ModelTierGeneral, Position: 1, LogicalModel: actualModel, Status: "eligible", SchedulableAccounts: 1}},
			ActualLogicalModel: &actualModel, ActualProviderModel: &providerModel,
			ChangeReason: "initial_selection", DecisionResult: service.RouteDecisionResultSelected,
			RoutingLatencyMS: 22, CreatedAt: now, UpdatedAt: now,
		},
		InferenceRun: &service.GatewayInferenceRunRecord{
			ID: classifierRunID, Purpose: "auto_complexity_classification", Profile: "auto_complexity",
			Backend: "synthetic-backend", Provider: "synthetic-provider", Model: "classifier-small",
			PromptVersion: service.AutoComplexityVersion, SchemaVersion: service.AutoComplexityVersion,
			Status: "completed", ProviderRequestID: testStringPtr("provider-request-synthetic"),
			InputUnits: &inputUnits, OutputUnits: &outputUnits, LatencyMS: 17, CreatedAt: now,
		},
	}
	committed, err := repo.PersistRouteDecision(ctx, write)
	require.NoError(t, err)
	require.True(t, committed)

	stale := write
	stale.Record.ID = uuid.New()
	stale.Record.GatewayRequestID = uuid.New()
	stale.Record.ClassifierRunID = nil
	stale.InferenceRun = nil
	committed, err = repo.PersistRouteDecision(ctx, stale)
	require.NoError(t, err)
	require.False(t, committed, "stale routing versions must not insert a decision")

	require.NoError(t, repo.FinalizeRouteDecision(ctx, decisionID, 1, "account_switch", now.Add(2*time.Second)))
	seedGatewayUsageUsage(t, ctx, db, gatewayID, userID, apiKeyID, accountID, actualModel, 20, 5, 0.01, now.Add(3*time.Second))

	decisions, metrics, err := repo.ListRouteDecisions(ctx, 10)
	require.NoError(t, err)
	require.Len(t, decisions, 1)
	require.True(t, decisions[0].AuditLinked)
	require.True(t, decisions[0].UsageLinked)
	require.NotNil(t, decisions[0].InferenceRun)
	require.Equal(t, classifierRunID, decisions[0].InferenceRun.ID)
	require.Equal(t, "synthetic-backend", decisions[0].InferenceRun.Backend)
	require.EqualValues(t, 12, *decisions[0].InferenceRun.InputUnits)
	require.Equal(t, int16(1), decisions[0].TechnicalRetryCount)
	require.Equal(t, "account_switch", *decisions[0].TechnicalRetryReason)
	require.Equal(t, int64(1), metrics.DecisionCount)
	require.Equal(t, int64(1), metrics.ClassifierCallCount)
	require.Equal(t, int64(17), metrics.ClassifierP95LatencyMS)

	snapshot, err = repo.LoadAutoRoutingSnapshot(ctx, session.ID, now.Add(4*time.Second))
	require.NoError(t, err)
	require.Equal(t, int64(1), snapshot.RoutingVersion)
	require.Equal(t, actualModel, snapshot.SelectedLogicalModel)
	require.Equal(t, service.ModelTierGeneral, snapshot.SelectedTier)
	require.Equal(t, []string{"tool_use"}, snapshot.RequiredCapabilities)

	var inferenceRunCount, routeDecisionCount, auditInteractionCount, workSessionCount, usageCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gateway_inference_runs`).Scan(&inferenceRunCount))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM route_decisions`).Scan(&routeDecisionCount))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM audit_interactions`).Scan(&auditInteractionCount))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM work_sessions`).Scan(&workSessionCount))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_logs WHERE gateway_request_id=$1`, gatewayID).Scan(&usageCount))
	require.Equal(t, 1, inferenceRunCount)
	require.Equal(t, 1, routeDecisionCount)
	require.Equal(t, 1, auditInteractionCount)
	require.Equal(t, 1, workSessionCount)
	require.Equal(t, 1, usageCount, "the only employee usage row is the explicitly seeded upstream result")
}

func testStringPtr(value string) *string { return &value }
