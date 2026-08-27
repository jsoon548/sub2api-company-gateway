package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

const gatewayUsageLinkedCTE = `
WITH usage_by_gateway AS (
    SELECT gateway_request_id,
           MIN(id) AS usage_log_id,
           COUNT(*)::integer AS usage_record_count,
           MIN(user_id) AS usage_user_id,
           MIN(account_id) AS account_id,
           MIN(COALESCE(NULLIF(TRIM(requested_model), ''), model)) AS requested_model,
           MIN(COALESCE(NULLIF(TRIM(upstream_model), ''), model)) AS resolved_model,
           SUM(input_tokens)::bigint AS input_tokens,
           SUM(output_tokens)::bigint AS output_tokens,
           SUM(input_tokens + output_tokens + cache_creation_tokens + cache_read_tokens)::bigint AS total_tokens,
           SUM(total_cost)::double precision AS total_cost,
           SUM(actual_cost)::double precision AS actual_cost,
           MAX(duration_ms) AS duration_ms,
           MIN(created_at) AS usage_created_at
    FROM usage_logs
    WHERE gateway_request_id IS NOT NULL
    GROUP BY gateway_request_id
), linked AS (
    SELECT COALESCE(a.gateway_request_id, u.gateway_request_id) AS gateway_request_id,
           a.id AS audit_interaction_id,
           u.usage_log_id,
           COALESCE(u.usage_record_count, 0) AS usage_record_count,
           (a.id IS NOT NULL) AS audit_present,
           (u.usage_log_id IS NOT NULL) AS usage_present,
           CASE
             WHEN a.id IS NULL THEN 'audit_failed'
             WHEN a.request_outcome = 'rejected_pre_upstream' THEN 'rejected_pre_upstream'
             WHEN a.content_state = 'incomplete' OR a.downstream_write_result = 'failed' THEN 'audit_failed'
             WHEN u.usage_log_id IS NULL THEN 'no_usage'
             ELSE 'normal_usage'
           END AS result,
           COALESCE(a.admitted_at, u.usage_created_at) AS event_time,
           COALESCE(a.subject_user_id, u.usage_user_id) AS subject_user_id,
           a.subject_email_snapshot,
           a.profile_version,
           a.protocol,
           a.transport,
           COALESCE(a.requested_model, u.requested_model) AS requested_model,
           COALESCE(a.resolved_model, u.resolved_model) AS resolved_model,
           a.request_outcome,
           a.content_state,
           u.account_id,
           u.input_tokens,
           u.output_tokens,
           u.total_tokens,
           u.total_cost,
           u.actual_cost,
           u.duration_ms,
           u.usage_created_at
    FROM audit_interactions a
    FULL OUTER JOIN usage_by_gateway u ON u.gateway_request_id = a.gateway_request_id
)
`

const gatewayUsageSelectColumns = `gateway_request_id,audit_interaction_id,usage_log_id,
usage_record_count,audit_present,usage_present,result,event_time,
subject_user_id,subject_email_snapshot,profile_version,protocol,transport,
requested_model,resolved_model,request_outcome,content_state,account_id,
input_tokens,output_tokens,total_tokens,total_cost,actual_cost,duration_ms,usage_created_at`

func gatewayUsageWhere(filter service.GatewayUsageFilter) (string, []any) {
	where := []string{"1=1"}
	args := make([]any, 0, 10)
	add := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	if filter.Employee != "" {
		placeholder := add(filter.Employee)
		where = append(where, "(subject_user_id::text = "+placeholder+" OR subject_email_snapshot ILIKE '%' || "+placeholder+" || '%')")
	}
	if filter.Profile != "" {
		where = append(where, "profile_version = "+add(filter.Profile))
	}
	if filter.Protocol != "" {
		where = append(where, "protocol = "+add(filter.Protocol))
	}
	if filter.Model != "" {
		placeholder := add(filter.Model)
		where = append(where, "(requested_model = "+placeholder+" OR resolved_model = "+placeholder+")")
	}
	if filter.Result != "" {
		where = append(where, "result = "+add(filter.Result))
	}
	if filter.RequestOutcome != "" {
		where = append(where, "request_outcome = "+add(filter.RequestOutcome))
	}
	if filter.ContentState != "" {
		where = append(where, "content_state = "+add(filter.ContentState))
	}
	if filter.From != nil {
		where = append(where, "event_time >= "+add(filter.From.UTC()))
	}
	if filter.To != nil {
		where = append(where, "event_time < "+add(filter.To.UTC()))
	}
	if filter.GatewayRequestID != nil {
		where = append(where, "gateway_request_id = "+add(*filter.GatewayRequestID))
	}
	return strings.Join(where, " AND "), args
}

func (r *auditManagementRepository) ListGatewayUsage(ctx context.Context, filter service.GatewayUsageFilter) (service.GatewayUsagePage, error) {
	where, args := gatewayUsageWhere(filter)
	var total int64
	if err := r.db.QueryRowContext(ctx, gatewayUsageLinkedCTE+"SELECT COUNT(*) FROM linked WHERE "+where, args...).Scan(&total); err != nil {
		return service.GatewayUsagePage{}, fmt.Errorf("count gateway usage: %w", err)
	}

	pageArgs := append([]any(nil), args...)
	limit := "$" + strconv.Itoa(len(pageArgs)+1)
	pageArgs = append(pageArgs, filter.PageSize)
	offset := "$" + strconv.Itoa(len(pageArgs)+1)
	pageArgs = append(pageArgs, (filter.Page-1)*filter.PageSize)
	rows, err := r.db.QueryContext(ctx, gatewayUsageLinkedCTE+
		"SELECT "+gatewayUsageSelectColumns+" FROM linked WHERE "+where+
		" ORDER BY event_time DESC,gateway_request_id DESC LIMIT "+limit+" OFFSET "+offset, pageArgs...)
	if err != nil {
		return service.GatewayUsagePage{}, fmt.Errorf("query gateway usage: %w", err)
	}
	defer rows.Close()

	items := make([]service.GatewayUsageRecord, 0, filter.PageSize)
	for rows.Next() {
		record, scanErr := scanGatewayUsageRecord(rows)
		if scanErr != nil {
			return service.GatewayUsagePage{}, fmt.Errorf("scan gateway usage: %w", scanErr)
		}
		items = append(items, record)
	}
	if err := rows.Err(); err != nil {
		return service.GatewayUsagePage{}, fmt.Errorf("iterate gateway usage: %w", err)
	}
	return service.GatewayUsagePage{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

func (r *auditManagementRepository) GetGatewayUsage(ctx context.Context, gatewayRequestID uuid.UUID) (service.GatewayUsageRecord, error) {
	row := r.db.QueryRowContext(ctx, gatewayUsageLinkedCTE+
		"SELECT "+gatewayUsageSelectColumns+" FROM linked WHERE gateway_request_id=$1", gatewayRequestID)
	record, err := scanGatewayUsageRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return service.GatewayUsageRecord{}, service.ErrGatewayUsageNotFound
	}
	if err != nil {
		return service.GatewayUsageRecord{}, fmt.Errorf("get gateway usage: %w", err)
	}
	return record, nil
}

func (r *auditManagementRepository) SummarizeGatewayUsage(ctx context.Context, filter service.GatewayUsageFilter, groupBy string) (service.GatewayUsageSummary, error) {
	where, args := gatewayUsageWhere(filter)
	var totals service.GatewayUsageTotals
	err := r.db.QueryRowContext(ctx, gatewayUsageLinkedCTE+`
SELECT COUNT(*),
       COALESCE(SUM(usage_record_count),0),
       COUNT(*) FILTER (WHERE result='normal_usage'),
       COUNT(*) FILTER (WHERE result='no_usage'),
       COUNT(*) FILTER (WHERE result='audit_failed'),
       COUNT(*) FILTER (WHERE result='rejected_pre_upstream'),
       COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),
       COALESCE(SUM(total_cost),0),COALESCE(SUM(actual_cost),0)
FROM linked WHERE `+where, args...).Scan(
		&totals.Requests, &totals.UsageRecords, &totals.NormalUsageRequests,
		&totals.NoUsageRequests, &totals.AuditFailedRequests, &totals.RejectedPreUpstreamRequests,
		&totals.InputTokens, &totals.OutputTokens, &totals.TotalCost, &totals.ActualCost,
	)
	if err != nil {
		return service.GatewayUsageSummary{}, fmt.Errorf("summarize gateway usage totals: %w", err)
	}

	groupExpression, err := gatewayUsageGroupExpression(groupBy)
	if err != nil {
		return service.GatewayUsageSummary{}, err
	}
	query := gatewayUsageLinkedCTE + `SELECT ` + groupExpression + ` AS group_key,
       COUNT(*),COALESCE(SUM(usage_record_count),0),
       COALESCE(SUM(input_tokens),0),COALESCE(SUM(output_tokens),0),
       COALESCE(SUM(total_cost),0),COALESCE(SUM(actual_cost),0)
FROM linked WHERE ` + where + ` GROUP BY ` + groupExpression + ` ORDER BY group_key ASC`
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return service.GatewayUsageSummary{}, fmt.Errorf("summarize gateway usage groups: %w", err)
	}
	defer rows.Close()
	items := make([]service.GatewayUsageAggregate, 0)
	for rows.Next() {
		var item service.GatewayUsageAggregate
		if err := rows.Scan(&item.Key, &item.Requests, &item.UsageRecords, &item.InputTokens, &item.OutputTokens, &item.TotalCost, &item.ActualCost); err != nil {
			return service.GatewayUsageSummary{}, fmt.Errorf("scan gateway usage group: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return service.GatewayUsageSummary{}, fmt.Errorf("iterate gateway usage groups: %w", err)
	}
	return service.GatewayUsageSummary{GroupBy: groupBy, Totals: totals, Items: items}, nil
}

func gatewayUsageGroupExpression(groupBy string) (string, error) {
	switch groupBy {
	case "time":
		return "TO_CHAR(event_time AT TIME ZONE 'UTC','YYYY-MM-DD')", nil
	case "employee":
		return "COALESCE(NULLIF(subject_email_snapshot,''),CASE WHEN subject_user_id IS NOT NULL THEN 'user:' || subject_user_id::text ELSE 'unknown' END)", nil
	case "profile":
		return "COALESCE(NULLIF(profile_version,''),'audit_unavailable')", nil
	case "model":
		return "COALESCE(NULLIF(requested_model,''),'unknown')", nil
	case "result":
		return "result", nil
	default:
		return "", fmt.Errorf("unsupported gateway usage group: %s", groupBy)
	}
}

type gatewayUsageScanner interface{ Scan(...any) error }

func scanGatewayUsageRecord(scanner gatewayUsageScanner) (service.GatewayUsageRecord, error) {
	var record service.GatewayUsageRecord
	var auditID uuid.NullUUID
	var usageLogID, subjectUserID, accountID sql.NullInt64
	var subjectEmail, profile, protocol, transport sql.NullString
	var requestedModel, resolvedModel, requestOutcome, contentState sql.NullString
	var inputTokens, outputTokens, totalTokens, durationMs sql.NullInt64
	var totalCost, actualCost sql.NullFloat64
	var usageCreatedAt sql.NullTime
	err := scanner.Scan(
		&record.GatewayRequestID, &auditID, &usageLogID,
		&record.UsageRecordCount, &record.AuditPresent, &record.UsagePresent,
		&record.Result, &record.EventTime, &subjectUserID, &subjectEmail,
		&profile, &protocol, &transport, &requestedModel, &resolvedModel,
		&requestOutcome, &contentState, &accountID, &inputTokens, &outputTokens,
		&totalTokens, &totalCost, &actualCost, &durationMs, &usageCreatedAt,
	)
	if err != nil {
		return record, err
	}
	if auditID.Valid {
		value := auditID.UUID
		record.AuditInteractionID = &value
	}
	record.UsageLogID = nullInt64ValuePtr(usageLogID)
	record.SubjectUserID = nullInt64ValuePtr(subjectUserID)
	record.AccountID = nullInt64ValuePtr(accountID)
	record.SubjectEmailSnapshot = nullStringValuePtr(subjectEmail)
	record.ProfileVersion = nullStringValuePtr(profile)
	record.Protocol = nullStringValuePtr(protocol)
	record.Transport = nullStringValuePtr(transport)
	record.RequestedModel = nullStringValuePtr(requestedModel)
	record.ResolvedModel = nullStringValuePtr(resolvedModel)
	record.RequestOutcome = nullStringValuePtr(requestOutcome)
	record.ContentState = nullStringValuePtr(contentState)
	record.InputTokens = nullInt64ValuePtr(inputTokens)
	record.OutputTokens = nullInt64ValuePtr(outputTokens)
	record.TotalTokens = nullInt64ValuePtr(totalTokens)
	record.TotalCost = nullFloat64ValuePtr(totalCost)
	record.ActualCost = nullFloat64ValuePtr(actualCost)
	if durationMs.Valid {
		value := int(durationMs.Int64)
		record.DurationMs = &value
	}
	if usageCreatedAt.Valid {
		value := usageCreatedAt.Time
		record.UsageCreatedAt = &value
	}
	return record, nil
}

func nullInt64ValuePtr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	out := value.Int64
	return &out
}

func nullStringValuePtr(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	out := value.String
	return &out
}

func nullFloat64ValuePtr(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	out := value.Float64
	return &out
}

var _ service.GatewayUsageRepository = (*auditManagementRepository)(nil)
