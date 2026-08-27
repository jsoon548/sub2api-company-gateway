//go:build auditFoundationmigration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestGatewayUsageCorrelationAgainstPostgres(t *testing.T) {
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

	seedAuditManagementSuperAdmin(t, ctx, db)
	userID := seedAuditManagementUser(t, ctx, db, service.RoleUser)
	accountID, apiKeyID := seedGatewayUsageUsageDependencies(t, ctx, db, userID)
	now := time.Now().UTC().Truncate(time.Microsecond)

	normalID := uuid.New()
	seedGatewayUsageAudit(t, ctx, db, normalID, userID, "historical-employee@example.invalid", "codex-openai-v1", "gatewayUsage-model", service.AuditRequestCompleted, service.AuditContentComplete, "succeeded", now.Add(-5*time.Hour))
	seedGatewayUsageUsage(t, ctx, db, normalID, userID, apiKeyID, accountID, "gatewayUsage-model", 120, 30, 0.25, now.Add(-5*time.Hour+time.Second))

	noUsageID := uuid.New()
	seedGatewayUsageAudit(t, ctx, db, noUsageID, userID, "historical-employee@example.invalid", "claude-code-anthropic-v1", "gatewayUsage-model", service.AuditRequestCompleted, service.AuditContentComplete, "succeeded", now.Add(-4*time.Hour))

	orphanUsageID := uuid.New()
	seedGatewayUsageUsage(t, ctx, db, orphanUsageID, userID, apiKeyID, accountID, "gatewayUsage-model", 5, 2, 0.01, now.Add(-3*time.Hour))

	rejectedID := uuid.New()
	seedGatewayUsageAudit(t, ctx, db, rejectedID, userID, "historical-employee@example.invalid", "codex-openai-v1", "blocked-model", service.AuditRequestRejectedPreUpstream, service.AuditContentRecording, "not_applicable", now.Add(-2*time.Hour))

	incompleteID := uuid.New()
	seedGatewayUsageAudit(t, ctx, db, incompleteID, userID, "historical-employee@example.invalid", "codex-openai-v1", "gatewayUsage-model", service.AuditRequestInterrupted, service.AuditContentIncomplete, "failed", now.Add(-time.Hour))

	_, err = db.ExecContext(ctx, `UPDATE users SET email='renamed-employee@example.invalid' WHERE id=$1`, userID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE accounts SET status='disabled' WHERE id=$1`, accountID)
	require.NoError(t, err)

	management := service.NewAuditManagementService(NewAuditManagementRepository(db), nil)
	page, err := management.ListGatewayUsage(ctx, service.GatewayUsageFilter{From: timePtr(now.Add(-24 * time.Hour)), To: timePtr(now.Add(time.Hour)), Page: 1, PageSize: 2})
	require.NoError(t, err)
	require.Equal(t, int64(5), page.Total)
	require.Len(t, page.Items, 2)
	lastPage, err := management.ListGatewayUsage(ctx, service.GatewayUsageFilter{From: timePtr(now.Add(-24 * time.Hour)), To: timePtr(now.Add(time.Hour)), Page: 3, PageSize: 2})
	require.NoError(t, err)
	require.Len(t, lastPage.Items, 1)

	normal, err := management.GetGatewayUsage(ctx, normalID)
	require.NoError(t, err)
	require.Equal(t, service.GatewayUsageResultNormalUsage, normal.Result)
	require.True(t, normal.AuditPresent)
	require.True(t, normal.UsagePresent)
	require.Equal(t, int64(150), *normal.TotalTokens)
	require.Equal(t, accountID, *normal.AccountID, "disabled account history must remain attributable")
	require.Equal(t, "historical-employee@example.invalid", *normal.SubjectEmailSnapshot, "current employee rename must not rewrite historical subject")

	noUsage, err := management.GetGatewayUsage(ctx, noUsageID)
	require.NoError(t, err)
	require.Equal(t, service.GatewayUsageResultNoUsage, noUsage.Result)
	require.Nil(t, noUsage.TotalTokens)
	require.Nil(t, noUsage.ActualCost)

	orphan, err := management.GetGatewayUsage(ctx, orphanUsageID)
	require.NoError(t, err)
	require.Equal(t, service.GatewayUsageResultAuditFailed, orphan.Result)
	require.False(t, orphan.AuditPresent)
	require.True(t, orphan.UsagePresent)

	rejected, err := management.GetGatewayUsage(ctx, rejectedID)
	require.NoError(t, err)
	require.Equal(t, service.GatewayUsageResultRejectedPreUpstream, rejected.Result)
	require.False(t, rejected.UsagePresent)

	incomplete, err := management.GetGatewayUsage(ctx, incompleteID)
	require.NoError(t, err)
	require.Equal(t, service.GatewayUsageResultAuditFailed, incomplete.Result)
	require.False(t, incomplete.UsagePresent)

	historical, err := management.ListGatewayUsage(ctx, service.GatewayUsageFilter{Employee: "historical-employee@example.invalid", Page: 1, PageSize: 100})
	require.NoError(t, err)
	require.Equal(t, int64(4), historical.Total)
	renamed, err := management.ListGatewayUsage(ctx, service.GatewayUsageFilter{Employee: "renamed-employee@example.invalid", Page: 1, PageSize: 100})
	require.NoError(t, err)
	require.Zero(t, renamed.Total)

	completed, err := management.ListGatewayUsage(ctx, service.GatewayUsageFilter{Protocol: "openai", RequestOutcome: service.AuditRequestCompleted, ContentState: service.AuditContentComplete, Page: 1, PageSize: 100})
	require.NoError(t, err)
	require.Equal(t, int64(2), completed.Total, "unified audit filters must preserve both usage and no-usage completed requests")
	for _, item := range completed.Items {
		require.Equal(t, "openai", *item.Protocol)
		require.Equal(t, service.AuditRequestCompleted, *item.RequestOutcome)
		require.Equal(t, service.AuditContentComplete, *item.ContentState)
	}

	for _, groupBy := range []string{"time", "employee", "profile", "model", "result"} {
		summary, summaryErr := management.SummarizeGatewayUsage(ctx, service.GatewayUsageFilter{From: timePtr(now.Add(-24 * time.Hour)), To: timePtr(now.Add(time.Hour))}, groupBy)
		require.NoError(t, summaryErr)
		require.Equal(t, int64(5), summary.Totals.Requests)
		require.Equal(t, int64(2), summary.Totals.UsageRecords)
		require.Equal(t, int64(1), summary.Totals.NormalUsageRequests)
		require.Equal(t, int64(1), summary.Totals.NoUsageRequests)
		require.Equal(t, int64(2), summary.Totals.AuditFailedRequests)
		require.Equal(t, int64(1), summary.Totals.RejectedPreUpstreamRequests)
		require.NotEmpty(t, summary.Items)
	}

	for i := 0; i < 125; i++ {
		seedGatewayUsageAudit(t, ctx, db, uuid.New(), userID, "historical-employee@example.invalid", "codex-openai-v1", "pagination-model", service.AuditRequestCompleted, service.AuditContentComplete, "succeeded", now.AddDate(0, 0, -400).Add(time.Duration(i)*time.Minute))
	}
	largeRange, err := management.ListGatewayUsage(ctx, service.GatewayUsageFilter{From: timePtr(now.AddDate(0, 0, -500)), To: timePtr(now.Add(time.Hour)), Page: 2, PageSize: 100})
	require.NoError(t, err)
	require.Equal(t, int64(130), largeRange.Total)
	require.Len(t, largeRange.Items, 30)

	encoded, err := json.Marshal(struct {
		Page    service.GatewayUsagePage    `json:"page"`
		Summary service.GatewayUsageSummary `json:"summary"`
	}{Page: page, Summary: mustGatewayUsageSummary(t, management, ctx, now)})
	require.NoError(t, err)
	lower := strings.ToLower(string(encoded))
	for _, forbidden := range []string{"api_key", "secret", "nonce", "ciphertext", "auth_tag", "plaintext", "raw_content"} {
		require.NotContains(t, lower, forbidden)
	}
}

func seedGatewayUsageUsageDependencies(t *testing.T, ctx context.Context, db *sql.DB, userID int64) (int64, int64) {
	t.Helper()
	var accountID, apiKeyID int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO accounts(name,platform,type,credentials,extra,concurrency,priority,status,created_at,updated_at)
VALUES($1,'openai','apikey','{}','{}',1,50,'active',NOW(),NOW()) RETURNING id`, "gatewayUsage-account-"+uuid.NewString()).Scan(&accountID))
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO api_keys(user_id,key,name,status,created_at,updated_at)
VALUES($1,$2,'gatewayUsage-key','active',NOW(),NOW()) RETURNING id`, userID, "sk-gatewayUsage-"+uuid.NewString()).Scan(&apiKeyID))
	return accountID, apiKeyID
}

func seedGatewayUsageAudit(t *testing.T, ctx context.Context, db *sql.DB, gatewayID uuid.UUID, userID int64, email, profile, model, outcome, contentState, writeResult string, admittedAt time.Time) {
	t.Helper()
	interactionID := uuid.New()
	_, err := db.ExecContext(ctx, `
INSERT INTO audit_interactions(
 id,gateway_request_id,subject_user_id,subject_email_snapshot,profile_version,protocol,
	 endpoint,method,transport,requested_model,resolved_model,
	 downstream_write_result,admitted_at,expires_at,last_activity_at,created_at,updated_at)
VALUES($1,$2,$3,$4,$5,'openai','/v1/responses','POST','http',$6,$6,'not_applicable',
	$7::timestamptz,$7::timestamptz+INTERVAL '180 days',$7::timestamptz,$7::timestamptz,$7::timestamptz)`,
		interactionID, gatewayID, userID, email, profile, model, admittedAt)
	require.NoError(t, err)
	contentVersion := 0
	if contentState != service.AuditContentRecording {
		contentVersion = 1
	}
	_, err = db.ExecContext(ctx, `
UPDATE audit_interactions
SET request_outcome=$2,request_outcome_version=1,
    content_state=$3,content_state_version=$4,
    downstream_write_result=$5,completed_at=$6,last_activity_at=$6
WHERE id=$1`, interactionID, outcome, contentState, contentVersion, writeResult, admittedAt)
	require.NoError(t, err)
}

func seedGatewayUsageUsage(t *testing.T, ctx context.Context, db *sql.DB, gatewayID uuid.UUID, userID, apiKeyID, accountID int64, model string, inputTokens, outputTokens int, actualCost float64, createdAt time.Time) {
	t.Helper()
	_, err := db.ExecContext(ctx, `
INSERT INTO usage_logs(user_id,api_key_id,account_id,request_id,model,requested_model,input_tokens,output_tokens,total_cost,actual_cost,duration_ms,gateway_request_id,created_at)
VALUES($1,$2,$3,$4,$5,$5,$6,$7,$8,$8,250,$9,$10)`, userID, apiKeyID, accountID, "gatewayUsage:"+gatewayID.String(), model, inputTokens, outputTokens, actualCost, gatewayID, createdAt)
	require.NoError(t, err)
}

func timePtr(value time.Time) *time.Time { return &value }

func mustGatewayUsageSummary(t *testing.T, management *service.AuditManagementService, ctx context.Context, now time.Time) service.GatewayUsageSummary {
	t.Helper()
	summary, err := management.SummarizeGatewayUsage(ctx, service.GatewayUsageFilter{From: timePtr(now.Add(-24 * time.Hour)), To: timePtr(now.Add(time.Hour))}, "result")
	require.NoError(t, err)
	return summary
}
