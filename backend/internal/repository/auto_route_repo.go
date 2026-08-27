package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

func (r *workSessionRepository) LoadAutoRoutingSnapshot(ctx context.Context, sessionID uuid.UUID, at time.Time) (service.AutoRoutingSnapshot, error) {
	if r == nil || r.db == nil || sessionID == uuid.Nil {
		return service.AutoRoutingSnapshot{}, service.ErrAutoRoutingSchema
	}
	var snapshot service.AutoRoutingSnapshot
	var selectedModel, selectedTier, selectedComplexity sql.NullString
	var requiredCapabilities []byte
	err := r.db.QueryRowContext(ctx, `
SELECT id,employee_user_id,profile_version,config_version,routing_version,
       selected_logical_model,selected_tier,selected_complexity,required_capabilities
FROM work_sessions
WHERE id=$1 AND reliability='reliable'`, sessionID).Scan(
		&snapshot.SessionID, &snapshot.EmployeeUserID, &snapshot.ProfileVersion,
		&snapshot.ConfigVersion, &snapshot.RoutingVersion, &selectedModel,
		&selectedTier, &selectedComplexity, &requiredCapabilities,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return service.AutoRoutingSnapshot{}, service.ErrAutoReliableRequired
	}
	if err != nil {
		return service.AutoRoutingSnapshot{}, err
	}
	if selectedModel.Valid {
		snapshot.SelectedLogicalModel = selectedModel.String
	}
	if selectedTier.Valid {
		snapshot.SelectedTier = selectedTier.String
	}
	if selectedComplexity.Valid {
		snapshot.SelectedComplexity = selectedComplexity.String
	}
	if err := json.Unmarshal(requiredCapabilities, &snapshot.RequiredCapabilities); err != nil {
		return service.AutoRoutingSnapshot{}, err
	}

	rows, err := r.db.QueryContext(ctx, `
SELECT p.tier,p.position,e.logical_model,e.provider_model,e.capabilities,
       GREATEST(p.valid_from,e.valid_from),
       CASE
         WHEN p.valid_until IS NULL THEN e.valid_until
         WHEN e.valid_until IS NULL THEN p.valid_until
         ELSE LEAST(p.valid_until,e.valid_until)
       END,
       EXISTS (
         SELECT 1 FROM model_catalog_entries emergency
         WHERE emergency.logical_model=e.logical_model AND emergency.emergency_disabled
       )
FROM auto_candidate_pools p
JOIN model_catalog_entries e ON e.id=p.catalog_entry_id AND e.generation=p.generation
WHERE p.generation=$1
ORDER BY CASE p.tier WHEN 'economy' THEN 1 WHEN 'general' THEN 2 ELSE 3 END,p.position`, snapshot.ConfigVersion)
	if err != nil {
		return service.AutoRoutingSnapshot{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var candidate service.AutoRouteCandidate
		var capabilities []byte
		var validUntil sql.NullTime
		if err := rows.Scan(
			&candidate.Tier, &candidate.Position, &candidate.LogicalModel,
			&candidate.ProviderModel, &capabilities, &candidate.ValidFrom,
			&validUntil, &candidate.EmergencyDisabled,
		); err != nil {
			return service.AutoRoutingSnapshot{}, err
		}
		if err := json.Unmarshal(capabilities, &candidate.Capabilities); err != nil {
			return service.AutoRoutingSnapshot{}, err
		}
		if validUntil.Valid {
			value := validUntil.Time
			candidate.ValidUntil = &value
		}
		snapshot.Candidates = append(snapshot.Candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return service.AutoRoutingSnapshot{}, err
	}
	return snapshot, nil
}

func (r *workSessionRepository) PersistRouteDecision(ctx context.Context, write service.RouteDecisionWrite) (bool, error) {
	if r == nil || r.db == nil || write.Record.ID == uuid.Nil || write.Record.GatewayRequestID == uuid.Nil || write.Record.WorkSessionID == uuid.Nil {
		return false, service.ErrAutoRoutingSchema
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	var currentVersion int64
	if err := tx.QueryRowContext(ctx, `SELECT routing_version FROM work_sessions WHERE id=$1 AND reliability='reliable' FOR UPDATE`, write.Record.WorkSessionID).Scan(&currentVersion); err != nil {
		return false, err
	}
	if currentVersion != write.ExpectedRoutingVersion {
		return false, nil
	}
	if write.Record.DecisionResult == service.RouteDecisionResultSelected {
		if write.Record.ActualLogicalModel == nil || write.Record.ActualProviderModel == nil {
			return false, service.ErrAutoRoutingSchema
		}
		capabilities, marshalErr := json.Marshal(write.SelectedCapabilities)
		if marshalErr != nil {
			return false, marshalErr
		}
		result, updateErr := tx.ExecContext(ctx, `
UPDATE work_sessions
SET selected_logical_model=$2,selected_tier=$3,selected_complexity=$4,
    required_capabilities=$5::jsonb,routing_version=routing_version+1
WHERE id=$1 AND routing_version=$6 AND reliability='reliable'`,
			write.Record.WorkSessionID, *write.Record.ActualLogicalModel, write.Record.EffectiveTier,
			write.Record.TaskComplexity, string(capabilities), write.ExpectedRoutingVersion,
		)
		if updateErr != nil {
			return false, updateErr
		}
		changed, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			return false, rowsErr
		}
		if changed != 1 {
			return false, nil
		}
	}

	requiredCapabilities, err := json.Marshal(write.Record.RequiredCapabilities)
	if err != nil {
		return false, err
	}
	candidatePool, err := json.Marshal(write.Record.CandidatePool)
	if err != nil {
		return false, err
	}
	if run := write.InferenceRun; run != nil {
		_, err = tx.ExecContext(ctx, `
INSERT INTO gateway_inference_runs (
    id,purpose,profile,backend,provider,model,prompt_version,schema_version,status,
    provider_request_id,input_units,output_units,latency_ms,created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			run.ID, run.Purpose, run.Profile, run.Backend, run.Provider, run.Model,
			run.PromptVersion, run.SchemaVersion, run.Status, run.ProviderRequestID,
			run.InputUnits, run.OutputUnits, run.LatencyMS, run.CreatedAt,
		)
		if err != nil {
			return false, err
		}
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO route_decisions (
    id,gateway_request_id,work_session_id,employee_user_id,profile_version,config_version,
    required_capabilities,task_complexity,certainty,explanation,decision_source,rule_version,
    classifier_run_id,classifier_version,classifier_status,classifier_latency_ms,requested_tier,effective_tier,
    candidate_pool,actual_logical_model,actual_provider_model,change_reason,
    technical_retry_count,technical_retry_reason,decision_result,routing_latency_ms,created_at,updated_at
) VALUES (
    $1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,
    $19::jsonb,$20,$21,$22,$23,$24,$25,$26,$27,$27
)`,
		write.Record.ID, write.Record.GatewayRequestID, write.Record.WorkSessionID,
		write.Record.EmployeeUserID, write.Record.ProfileVersion, write.Record.ConfigVersion,
		string(requiredCapabilities), write.Record.TaskComplexity, write.Record.Certainty,
		write.Record.Explanation, write.Record.DecisionSource, write.Record.RuleVersion,
		write.Record.ClassifierRunID, write.Record.ClassifierVersion, write.Record.ClassifierStatus, write.Record.ClassifierLatencyMS,
		write.Record.RequestedTier, write.Record.EffectiveTier, string(candidatePool),
		write.Record.ActualLogicalModel, write.Record.ActualProviderModel, write.Record.ChangeReason,
		write.Record.TechnicalRetryCount, write.Record.TechnicalRetryReason,
		write.Record.DecisionResult, write.Record.RoutingLatencyMS, write.Record.CreatedAt,
	)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *workSessionRepository) FinalizeRouteDecision(ctx context.Context, decisionID uuid.UUID, retryCount int16, retryReason string, at time.Time) error {
	if retryCount < 0 || retryCount > 1 {
		return service.ErrWorkSessionInvalid
	}
	var reason any
	if retryCount == 1 {
		reason = retryReason
	}
	result, err := r.db.ExecContext(ctx, `
UPDATE route_decisions
SET technical_retry_count=$2,technical_retry_reason=$3,updated_at=$4
WHERE id=$1`, decisionID, retryCount, reason, at)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return service.ErrAutoRoutingSchema
	}
	return nil
}

func (r *workSessionRepository) ListRouteDecisions(ctx context.Context, limit int) ([]service.RouteDecisionRecord, service.AutoRoutingMetrics, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT d.id,d.gateway_request_id,d.work_session_id,d.employee_user_id,d.profile_version,d.config_version,
       d.required_capabilities,d.task_complexity,d.certainty,d.explanation,d.decision_source,d.rule_version,
       d.classifier_run_id,d.classifier_version,d.classifier_status,d.classifier_latency_ms,d.requested_tier,d.effective_tier,
       d.candidate_pool,d.actual_logical_model,d.actual_provider_model,d.change_reason,
       d.technical_retry_count,d.technical_retry_reason,d.decision_result,d.routing_latency_ms,
       TRUE,EXISTS(SELECT 1 FROM usage_logs u WHERE u.gateway_request_id=d.gateway_request_id),
       r.purpose,r.profile,r.backend,r.provider,r.model,r.prompt_version,r.schema_version,r.status,
       r.provider_request_id,r.input_units,r.output_units,r.latency_ms,r.created_at,
       d.created_at,d.updated_at
FROM route_decisions d
LEFT JOIN gateway_inference_runs r ON r.id=d.classifier_run_id
ORDER BY d.created_at DESC,d.id DESC
LIMIT $1`, limit)
	if err != nil {
		return nil, service.AutoRoutingMetrics{}, err
	}
	defer rows.Close()
	decisions := make([]service.RouteDecisionRecord, 0, limit)
	for rows.Next() {
		var record service.RouteDecisionRecord
		var requiredCapabilities, candidatePool []byte
		var classifierRunID uuid.NullUUID
		var classifierVersion, actualModel, providerModel, retryReason sql.NullString
		var runPurpose, runProfile, runBackend, runProvider, runModel sql.NullString
		var runPromptVersion, runSchemaVersion, runStatus, runProviderRequestID sql.NullString
		var runInput, runOutput, runLatency sql.NullInt64
		var runCreatedAt sql.NullTime
		if err := rows.Scan(
			&record.ID, &record.GatewayRequestID, &record.WorkSessionID, &record.EmployeeUserID,
			&record.ProfileVersion, &record.ConfigVersion, &requiredCapabilities,
			&record.TaskComplexity, &record.Certainty, &record.Explanation, &record.DecisionSource,
			&record.RuleVersion, &classifierRunID, &classifierVersion, &record.ClassifierStatus,
			&record.ClassifierLatencyMS, &record.RequestedTier, &record.EffectiveTier,
			&candidatePool, &actualModel, &providerModel, &record.ChangeReason,
			&record.TechnicalRetryCount, &retryReason, &record.DecisionResult,
			&record.RoutingLatencyMS, &record.AuditLinked, &record.UsageLinked,
			&runPurpose, &runProfile, &runBackend, &runProvider, &runModel,
			&runPromptVersion, &runSchemaVersion, &runStatus, &runProviderRequestID,
			&runInput, &runOutput, &runLatency, &runCreatedAt,
			&record.CreatedAt, &record.UpdatedAt,
		); err != nil {
			return nil, service.AutoRoutingMetrics{}, err
		}
		if err := json.Unmarshal(requiredCapabilities, &record.RequiredCapabilities); err != nil {
			return nil, service.AutoRoutingMetrics{}, err
		}
		if err := json.Unmarshal(candidatePool, &record.CandidatePool); err != nil {
			return nil, service.AutoRoutingMetrics{}, err
		}
		assignNullableString(&record.ClassifierVersion, classifierVersion)
		if classifierRunID.Valid {
			runID := classifierRunID.UUID
			record.ClassifierRunID = &runID
		}
		assignNullableString(&record.ActualLogicalModel, actualModel)
		assignNullableString(&record.ActualProviderModel, providerModel)
		assignNullableString(&record.TechnicalRetryReason, retryReason)
		if classifierRunID.Valid && runPurpose.Valid {
			run := &service.GatewayInferenceRunRecord{
				ID: classifierRunID.UUID, Purpose: runPurpose.String, Profile: runProfile.String,
				Backend: runBackend.String, Provider: runProvider.String, Model: runModel.String,
				PromptVersion: runPromptVersion.String, SchemaVersion: runSchemaVersion.String,
				Status: runStatus.String, LatencyMS: runLatency.Int64, CreatedAt: runCreatedAt.Time,
			}
			assignNullableString(&run.ProviderRequestID, runProviderRequestID)
			assignNullableInt64(&run.InputUnits, runInput)
			assignNullableInt64(&run.OutputUnits, runOutput)
			record.InferenceRun = run
		}
		decisions = append(decisions, record)
	}
	if err := rows.Err(); err != nil {
		return nil, service.AutoRoutingMetrics{}, err
	}

	var metrics service.AutoRoutingMetrics
	err = r.db.QueryRowContext(ctx, `
SELECT COUNT(*),
       COUNT(*) FILTER (WHERE classifier_run_id IS NOT NULL),
       COUNT(*) FILTER (WHERE classifier_status='timeout'),
       COUNT(*) FILTER (WHERE decision_source='fallback' AND certainty='uncertain'),
       COALESCE(percentile_disc(0.95) WITHIN GROUP (ORDER BY classifier_latency_ms)
         FILTER (WHERE classifier_run_id IS NOT NULL),0)::BIGINT,
       COALESCE(percentile_disc(0.95) WITHIN GROUP (ORDER BY routing_latency_ms),0)::BIGINT
FROM route_decisions`).Scan(
		&metrics.DecisionCount, &metrics.ClassifierCallCount, &metrics.ClassifierTimeoutCount,
		&metrics.ClassifierFallbackCount, &metrics.ClassifierP95LatencyMS,
		&metrics.RoutingP95LatencyMS,
	)
	if err != nil {
		return nil, service.AutoRoutingMetrics{}, fmt.Errorf("load Auto routing metrics: %w", err)
	}
	return decisions, metrics, nil
}

func assignNullableString(target **string, value sql.NullString) {
	if !value.Valid {
		return
	}
	copy := value.String
	*target = &copy
}

func assignNullableInt64(target **int64, value sql.NullInt64) {
	if !value.Valid {
		return
	}
	copy := value.Int64
	*target = &copy
}
