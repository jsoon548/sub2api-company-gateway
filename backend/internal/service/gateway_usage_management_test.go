package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestGatewayUsagePresenceQuadrantsAndResultPrecedence(t *testing.T) {
	tests := []struct {
		name                                         string
		audit, usage                                 bool
		outcome, contentState, downstreamWriteResult string
		want                                         string
	}{
		{name: "usage_and_audit", audit: true, usage: true, outcome: AuditRequestCompleted, contentState: AuditContentComplete, downstreamWriteResult: "succeeded", want: GatewayUsageResultNormalUsage},
		{name: "no_usage_with_audit", audit: true, usage: false, outcome: AuditRequestCompleted, contentState: AuditContentComplete, downstreamWriteResult: "succeeded", want: GatewayUsageResultNoUsage},
		{name: "usage_without_audit", audit: false, usage: true, want: GatewayUsageResultAuditFailed},
		{name: "neither_usage_nor_audit", audit: false, usage: false, want: GatewayUsageResultAuditFailed},
		{name: "pre_upstream_rejection_is_distinct", audit: true, usage: false, outcome: AuditRequestRejectedPreUpstream, contentState: AuditContentRecording, want: GatewayUsageResultRejectedPreUpstream},
		{name: "incomplete_audit_is_not_no_usage", audit: true, usage: false, outcome: AuditRequestInterrupted, contentState: AuditContentIncomplete, downstreamWriteResult: "failed", want: GatewayUsageResultAuditFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, classifyGatewayUsageResult(test.audit, test.usage, test.outcome, test.contentState, test.downstreamWriteResult))
		})
	}
}

func TestGatewayUsageMissingValuesAreOmittedInsteadOfSerializedAsZero(t *testing.T) {
	record := GatewayUsageRecord{
		GatewayRequestID: uuid.New(), AuditPresent: true, UsagePresent: false,
		Result: GatewayUsageResultNoUsage,
	}
	encoded, err := json.Marshal(record)
	require.NoError(t, err)
	text := string(encoded)
	for _, absent := range []string{"input_tokens", "output_tokens", "total_tokens", "total_cost", "actual_cost", "account_id", "usage_log_id"} {
		require.NotContains(t, text, absent)
	}
	for _, forbidden := range []string{"api_key", "secret", "nonce", "ciphertext", "auth_tag", "raw_content"} {
		require.NotContains(t, strings.ToLower(text), forbidden)
	}

	zero := int64(0)
	zeroCost := 0.0
	record.UsagePresent = true
	record.InputTokens = &zero
	record.TotalCost = &zeroCost
	encoded, err = json.Marshal(record)
	require.NoError(t, err)
	require.Contains(t, string(encoded), `"input_tokens":0`)
	require.Contains(t, string(encoded), `"total_cost":0`)
}

func TestGatewayUsageCorrelationDoesNotRewriteLegacyRequestID(t *testing.T) {
	gatewayID := uuid.New()
	ctx := context.WithValue(context.Background(), ctxkey.GatewayRequestID, gatewayID)
	ctx = context.WithValue(ctx, ctxkey.ClientRequestID, "synthetic-client-request")

	correlated := usageGatewayRequestIDFromContext(ctx)
	require.NotNil(t, correlated)
	require.Equal(t, gatewayID, *correlated)
	require.Equal(t, "client:synthetic-client-request", resolveUsageBillingRequestID(ctx, "synthetic-upstream-request"))
}

func TestGatewayUsageFilterIncludesSafeAuditDimensions(t *testing.T) {
	filter, err := normalizeGatewayUsageFilter(GatewayUsageFilter{
		Protocol: " OpenAI ", RequestOutcome: " COMPLETED ", ContentState: " COMPLETE ",
	})
	require.NoError(t, err)
	require.Equal(t, "openai", filter.Protocol)
	require.Equal(t, AuditRequestCompleted, filter.RequestOutcome)
	require.Equal(t, AuditContentComplete, filter.ContentState)

	for _, invalid := range []GatewayUsageFilter{
		{Protocol: "unknown"},
		{RequestOutcome: "successful"},
		{ContentState: "plaintext"},
	} {
		_, err = normalizeGatewayUsageFilter(invalid)
		require.Error(t, err)
	}
}
