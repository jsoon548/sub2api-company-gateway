package service

import (
	"context"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const (
	GatewayUsageResultNormalUsage         = "normal_usage"
	GatewayUsageResultNoUsage             = "no_usage"
	GatewayUsageResultAuditFailed         = "audit_failed"
	GatewayUsageResultRejectedPreUpstream = "rejected_pre_upstream"
)

var ErrGatewayUsageNotFound = infraerrors.NotFound("GATEWAY_USAGE_NOT_FOUND", "gateway usage detail not found")

// GatewayUsageFilter is limited to immutable/safe facts. It never accepts raw
// content, API-key material, ciphertext, or derived semantic-analysis fields.
type GatewayUsageFilter struct {
	Employee         string
	Profile          string
	Protocol         string
	Model            string
	Result           string
	RequestOutcome   string
	ContentState     string
	From             *time.Time
	To               *time.Time
	GatewayRequestID *uuid.UUID
	Page             int
	PageSize         int
}

// GatewayUsageRecord correlates one Gateway request with its audit metadata
// and optional usage fact. Usage numbers are pointers so a missing usage row is
// never serialized as a successful request with zero consumption.
type GatewayUsageRecord struct {
	GatewayRequestID     uuid.UUID  `json:"gateway_request_id"`
	AuditInteractionID   *uuid.UUID `json:"audit_interaction_id,omitempty"`
	UsageLogID           *int64     `json:"usage_log_id,omitempty"`
	UsageRecordCount     int        `json:"usage_record_count"`
	AuditPresent         bool       `json:"audit_present"`
	UsagePresent         bool       `json:"usage_present"`
	Result               string     `json:"result"`
	EventTime            time.Time  `json:"event_time"`
	SubjectUserID        *int64     `json:"subject_user_id,omitempty"`
	SubjectEmailSnapshot *string    `json:"subject_email_snapshot,omitempty"`
	ProfileVersion       *string    `json:"profile_version,omitempty"`
	Protocol             *string    `json:"protocol,omitempty"`
	Transport            *string    `json:"transport,omitempty"`
	RequestedModel       *string    `json:"requested_model,omitempty"`
	ResolvedModel        *string    `json:"resolved_model,omitempty"`
	RequestOutcome       *string    `json:"request_outcome,omitempty"`
	ContentState         *string    `json:"content_state,omitempty"`
	AccountID            *int64     `json:"account_id,omitempty"`
	InputTokens          *int64     `json:"input_tokens,omitempty"`
	OutputTokens         *int64     `json:"output_tokens,omitempty"`
	TotalTokens          *int64     `json:"total_tokens,omitempty"`
	TotalCost            *float64   `json:"total_cost,omitempty"`
	ActualCost           *float64   `json:"actual_cost,omitempty"`
	DurationMs           *int       `json:"duration_ms,omitempty"`
	UsageCreatedAt       *time.Time `json:"usage_created_at,omitempty"`
}

type GatewayUsagePage struct {
	Items    []GatewayUsageRecord `json:"items"`
	Total    int64                `json:"total"`
	Page     int                  `json:"page"`
	PageSize int                  `json:"page_size"`
}

type GatewayUsageAggregate struct {
	Key          string  `json:"key"`
	Requests     int64   `json:"requests"`
	UsageRecords int64   `json:"usage_records"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalCost    float64 `json:"total_cost"`
	ActualCost   float64 `json:"actual_cost"`
}

type GatewayUsageTotals struct {
	Requests                    int64   `json:"requests"`
	UsageRecords                int64   `json:"usage_records"`
	NormalUsageRequests         int64   `json:"normal_usage_requests"`
	NoUsageRequests             int64   `json:"no_usage_requests"`
	AuditFailedRequests         int64   `json:"audit_failed_requests"`
	RejectedPreUpstreamRequests int64   `json:"rejected_pre_upstream_requests"`
	InputTokens                 int64   `json:"input_tokens"`
	OutputTokens                int64   `json:"output_tokens"`
	TotalCost                   float64 `json:"total_cost"`
	ActualCost                  float64 `json:"actual_cost"`
}

type GatewayUsageSummary struct {
	GroupBy string                  `json:"group_by"`
	Totals  GatewayUsageTotals      `json:"totals"`
	Items   []GatewayUsageAggregate `json:"items"`
}

type GatewayUsageRepository interface {
	ListGatewayUsage(context.Context, GatewayUsageFilter) (GatewayUsagePage, error)
	GetGatewayUsage(context.Context, uuid.UUID) (GatewayUsageRecord, error)
	SummarizeGatewayUsage(context.Context, GatewayUsageFilter, string) (GatewayUsageSummary, error)
}

func classifyGatewayUsageResult(auditPresent, usagePresent bool, requestOutcome, contentState, downstreamWriteResult string) string {
	if !auditPresent {
		return GatewayUsageResultAuditFailed
	}
	if requestOutcome == AuditRequestRejectedPreUpstream {
		return GatewayUsageResultRejectedPreUpstream
	}
	if contentState == AuditContentIncomplete || downstreamWriteResult == "failed" {
		return GatewayUsageResultAuditFailed
	}
	if !usagePresent {
		return GatewayUsageResultNoUsage
	}
	return GatewayUsageResultNormalUsage
}

func normalizeGatewayUsageFilter(filter GatewayUsageFilter) (GatewayUsageFilter, error) {
	filter.Employee = strings.TrimSpace(filter.Employee)
	filter.Profile = strings.TrimSpace(filter.Profile)
	filter.Protocol = strings.ToLower(strings.TrimSpace(filter.Protocol))
	filter.Model = strings.TrimSpace(filter.Model)
	filter.Result = strings.ToLower(strings.TrimSpace(filter.Result))
	filter.RequestOutcome = strings.ToLower(strings.TrimSpace(filter.RequestOutcome))
	filter.ContentState = strings.ToLower(strings.TrimSpace(filter.ContentState))
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 {
		filter.PageSize = 20
	}
	if filter.PageSize > 100 {
		filter.PageSize = 100
	}
	if len(filter.Employee) > 255 || len(filter.Profile) > 64 || len(filter.Model) > 100 ||
		!validOptionalValue(filter.Protocol, "anthropic", "openai") ||
		!validOptionalValue(filter.Result,
			GatewayUsageResultNormalUsage,
			GatewayUsageResultNoUsage,
			GatewayUsageResultAuditFailed,
			GatewayUsageResultRejectedPreUpstream,
		) ||
		!validOptionalValue(filter.RequestOutcome, AuditRequestProcessing, AuditRequestRejectedPreUpstream, AuditRequestCompleted, AuditRequestUpstreamFailed, AuditRequestInterrupted) ||
		!validOptionalValue(filter.ContentState, AuditContentRecording, AuditContentComplete, AuditContentIncomplete, AuditContentExpired) ||
		(filter.From != nil && filter.To != nil && !filter.From.Before(*filter.To)) {
		return GatewayUsageFilter{}, infraerrors.BadRequest("GATEWAY_USAGE_FILTER_INVALID", "invalid gateway usage filter")
	}
	return filter, nil
}

func (s *AuditManagementService) ListGatewayUsage(ctx context.Context, filter GatewayUsageFilter) (GatewayUsagePage, error) {
	if s == nil || s.gatewayUsageRepo == nil {
		return GatewayUsagePage{}, errorsNewGatewayUsageRepositoryUnavailable()
	}
	filter, err := normalizeGatewayUsageFilter(filter)
	if err != nil {
		return GatewayUsagePage{}, err
	}
	return s.gatewayUsageRepo.ListGatewayUsage(ctx, filter)
}

func (s *AuditManagementService) GetGatewayUsage(ctx context.Context, gatewayRequestID uuid.UUID) (GatewayUsageRecord, error) {
	if s == nil || s.gatewayUsageRepo == nil {
		return GatewayUsageRecord{}, errorsNewGatewayUsageRepositoryUnavailable()
	}
	if gatewayRequestID == uuid.Nil {
		return GatewayUsageRecord{}, ErrGatewayUsageNotFound
	}
	return s.gatewayUsageRepo.GetGatewayUsage(ctx, gatewayRequestID)
}

func (s *AuditManagementService) SummarizeGatewayUsage(ctx context.Context, filter GatewayUsageFilter, groupBy string) (GatewayUsageSummary, error) {
	if s == nil || s.gatewayUsageRepo == nil {
		return GatewayUsageSummary{}, errorsNewGatewayUsageRepositoryUnavailable()
	}
	filter, err := normalizeGatewayUsageFilter(filter)
	if err != nil {
		return GatewayUsageSummary{}, err
	}
	groupBy = strings.ToLower(strings.TrimSpace(groupBy))
	if !validOptionalValue(groupBy, "time", "employee", "profile", "model", "result") {
		return GatewayUsageSummary{}, infraerrors.BadRequest("GATEWAY_USAGE_GROUP_INVALID", "invalid gateway usage group")
	}
	if groupBy == "" {
		groupBy = "time"
	}
	return s.gatewayUsageRepo.SummarizeGatewayUsage(ctx, filter, groupBy)
}

func errorsNewGatewayUsageRepositoryUnavailable() error {
	return infraerrors.ServiceUnavailable("GATEWAY_USAGE_UNAVAILABLE", "gateway usage repository unavailable")
}
