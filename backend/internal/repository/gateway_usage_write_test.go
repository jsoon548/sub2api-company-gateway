package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUsageLogInsertCarriesGatewayRequestIDWithoutReplacingRequestID(t *testing.T) {
	gatewayID := uuid.New()
	log := &service.UsageLog{
		UserID: 1, APIKeyID: 2, AccountID: 3,
		RequestID: "legacy-request-id", GatewayRequestID: &gatewayID,
		Model: "gatewayUsage-model", CreatedAt: time.Now().UTC(),
	}
	prepared := prepareUsageLogInsert(log)
	require.Equal(t, "legacy-request-id", prepared.requestID)
	require.Equal(t, &gatewayID, prepared.args[len(prepared.args)-2])
	require.Len(t, prepared.args, len(usageLogInsertArgTypes))

	query, args := buildUsageLogBestEffortInsertQuery([]usageLogInsertPrepared{prepared})
	require.Contains(t, query, "gateway_request_id")
	require.Contains(t, usageLogSelectColumns, "gateway_request_id")
	require.Equal(t, "legacy-request-id", args[3])
	require.Equal(t, &gatewayID, args[len(args)-2])
}
