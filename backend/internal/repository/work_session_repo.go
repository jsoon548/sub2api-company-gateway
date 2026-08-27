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

type workSessionRepository struct{ db *sql.DB }

func NewWorkSessionRepository(db *sql.DB) service.WorkSessionRepository {
	return &workSessionRepository{db: db}
}

func (r *workSessionRepository) CheckFoundation(ctx context.Context) error {
	if r == nil || r.db == nil {
		return service.ErrWorkSessionSchema
	}
	var workSessions, catalog, pools, routeDecisions, inferenceRuns bool
	err := r.db.QueryRowContext(ctx, `
SELECT to_regclass('work_sessions') IS NOT NULL,
       to_regclass('model_catalog_entries') IS NOT NULL,
	       to_regclass('auto_candidate_pools') IS NOT NULL,
	       to_regclass('route_decisions') IS NOT NULL,
	       to_regclass('gateway_inference_runs') IS NOT NULL`).Scan(&workSessions, &catalog, &pools, &routeDecisions, &inferenceRuns)
	if err != nil || !workSessions || !catalog || !pools || !routeDecisions || !inferenceRuns {
		return service.ErrWorkSessionSchema
	}
	return nil
}

func (r *workSessionRepository) CurrentGeneration(ctx context.Context) (int64, error) {
	cfg, err := r.GetAutoConfig(ctx)
	return cfg.ConfigVersion, err
}

func (r *workSessionRepository) GetAutoConfig(ctx context.Context) (service.AutoConfig, error) {
	var raw string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1`, service.WorkSessionAutoSettingKey).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return service.AutoConfig{ConfigVersion: 1}, nil
	}
	if err != nil {
		return service.AutoConfig{}, fmt.Errorf("load Auto configuration: %w", err)
	}
	return service.DecodeAutoConfig(raw), nil
}

func (r *workSessionRepository) FindOrCreateReliable(ctx context.Context, in service.WorkSessionCreate) (service.WorkSessionRecord, error) {
	row := r.db.QueryRowContext(ctx, `
INSERT INTO work_sessions (
    id,tenant_id,employee_user_id,profile_version,signal_source,signal_status,
    session_key_hmac,hmac_key_version,reliability,routing_mode,config_version,
    analysis_eligible,quota_grace_eligible,status,first_gateway_request_id,
    last_gateway_request_id,created_at,last_activity_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'reliable',$9,$10,TRUE,FALSE,'active',$11,$11,$12,$12)
ON CONFLICT (tenant_id,employee_user_id,profile_version,signal_source,hmac_key_version,session_key_hmac)
WHERE reliability='reliable'
DO UPDATE SET last_gateway_request_id=EXCLUDED.last_gateway_request_id,
              last_activity_at=EXCLUDED.last_activity_at
	RETURNING id,tenant_id,employee_user_id,profile_version,signal_source,signal_status,
	          hmac_key_version,reliability,routing_mode,config_version,analysis_eligible,
	          quota_grace_eligible,status,selected_logical_model,selected_tier,selected_complexity,
	          required_capabilities,routing_version,first_gateway_request_id,last_gateway_request_id,
	          created_at,last_activity_at`,
		in.ID, in.TenantID, in.EmployeeUserID, in.ProfileVersion, in.SignalSource,
		in.SignalStatus, in.SessionKeyHMAC, in.HMACKeyVersion, in.RoutingMode,
		in.ConfigVersion, in.GatewayRequestID, in.At,
	)
	return scanWorkSession(row)
}

func (r *workSessionRepository) CreateUnreliable(ctx context.Context, in service.WorkSessionCreate) (service.WorkSessionRecord, error) {
	row := r.db.QueryRowContext(ctx, `
INSERT INTO work_sessions (
    id,tenant_id,employee_user_id,profile_version,signal_source,signal_status,
    session_key_hmac,hmac_key_version,reliability,routing_mode,config_version,
    analysis_eligible,quota_grace_eligible,status,first_gateway_request_id,
    last_gateway_request_id,created_at,last_activity_at
) VALUES ($1,$2,$3,$4,$5,$6,NULL,NULL,'unreliable',$7,$8,FALSE,FALSE,'request_scoped',$9,$9,$10,$10)
	RETURNING id,tenant_id,employee_user_id,profile_version,signal_source,signal_status,
	          hmac_key_version,reliability,routing_mode,config_version,analysis_eligible,
	          quota_grace_eligible,status,selected_logical_model,selected_tier,selected_complexity,
	          required_capabilities,routing_version,first_gateway_request_id,last_gateway_request_id,
	          created_at,last_activity_at`,
		in.ID, in.TenantID, in.EmployeeUserID, in.ProfileVersion, in.SignalSource,
		in.SignalStatus, in.RoutingMode, in.ConfigVersion, in.GatewayRequestID, in.At,
	)
	return scanWorkSession(row)
}

type workSessionScanner interface{ Scan(...any) error }

func scanWorkSession(scanner workSessionScanner) (service.WorkSessionRecord, error) {
	var record service.WorkSessionRecord
	var keyVersion, selectedModel, selectedTier, selectedComplexity sql.NullString
	var requiredCapabilities []byte
	err := scanner.Scan(
		&record.ID, &record.TenantID, &record.EmployeeUserID, &record.ProfileVersion,
		&record.SignalSource, &record.SignalStatus, &keyVersion, &record.Reliability,
		&record.RoutingMode, &record.ConfigVersion, &record.AnalysisEligible,
		&record.QuotaGraceEligible, &record.Status, &selectedModel, &selectedTier, &selectedComplexity,
		&requiredCapabilities, &record.RoutingVersion, &record.FirstGatewayRequestID,
		&record.LastGatewayRequestID, &record.CreatedAt, &record.LastActivityAt,
	)
	if keyVersion.Valid {
		record.HMACKeyVersion = &keyVersion.String
	}
	if selectedModel.Valid {
		record.SelectedLogicalModel = &selectedModel.String
	}
	if selectedTier.Valid {
		record.SelectedTier = &selectedTier.String
	}
	if selectedComplexity.Valid {
		record.SelectedComplexity = &selectedComplexity.String
	}
	if err == nil {
		err = json.Unmarshal(requiredCapabilities, &record.RequiredCapabilities)
	}
	return record, err
}

func (r *workSessionRepository) LinkGatewayRequest(ctx context.Context, gatewayRequestID, sessionID uuid.UUID) error {
	result, err := r.db.ExecContext(ctx, `
UPDATE audit_interactions
SET work_session_id=$2,updated_at=NOW()
WHERE gateway_request_id=$1 AND work_session_id IS NULL`, gatewayRequestID, sessionID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return service.ErrWorkSessionSchema
	}
	return nil
}

func (r *workSessionRepository) ReplaceManagementConfig(ctx context.Context, input service.WorkSessionManagementUpdate, now time.Time) (int64, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	var raw string
	err = tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=$1 FOR UPDATE`, service.WorkSessionAutoSettingKey).Scan(&raw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	current := service.DecodeAutoConfig(raw)
	generation := current.ConfigVersion + 1
	entries := make(map[string]uuid.UUID, len(input.Catalog))
	for _, item := range input.Catalog {
		id := uuid.New()
		entries[item.LogicalModel] = id
		itemCapabilities := item.Capabilities
		if itemCapabilities == nil {
			itemCapabilities = []string{}
		}
		capabilities, marshalErr := json.Marshal(itemCapabilities)
		if marshalErr != nil {
			return 0, marshalErr
		}
		_, err = tx.ExecContext(ctx, `
INSERT INTO model_catalog_entries (
    id,generation,logical_model,provider_model,tier,capabilities,valid_from,valid_until,
    emergency_disabled,created_at,updated_at
) VALUES (
    $1,$2,$3,$4,$5,$6::jsonb,$7,$8,
    (SELECT COALESCE(BOOL_OR(emergency_disabled),FALSE) FROM model_catalog_entries WHERE logical_model=$3::varchar(100)),
    $9,$9
)`,
			id, generation, item.LogicalModel, item.ProviderModel, item.Tier,
			string(capabilities), item.ValidFrom.UTC(), item.ValidUntil, now,
		)
		if err != nil {
			return 0, err
		}
	}
	for _, pool := range input.CandidatePools {
		for index, model := range pool.Candidates {
			_, err = tx.ExecContext(ctx, `
INSERT INTO auto_candidate_pools (
    id,generation,tier,position,catalog_entry_id,valid_from,valid_until,created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
				uuid.New(), generation, pool.Tier, index+1, entries[model],
				pool.ValidFrom.UTC(), pool.ValidUntil, now,
			)
			if err != nil {
				return 0, err
			}
		}
	}
	config := service.AutoConfig{
		Enabled: input.AutoEnabled, UserWhitelist: input.UserWhitelist,
		GroupWhitelist: input.GroupWhitelist, ConfigVersion: generation,
	}
	encoded, err := service.EncodeAutoConfig(config)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO settings (key,value,updated_at) VALUES ($1,$2,$3)
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value,updated_at=EXCLUDED.updated_at`,
		service.WorkSessionAutoSettingKey, encoded, now,
	)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return generation, nil
}

func (r *workSessionRepository) ListManagement(ctx context.Context, sessionLimit int) (service.AutoConfig, []service.ModelCatalogEntry, []service.AutoCandidate, []service.WorkSessionRecord, error) {
	auto, err := r.GetAutoConfig(ctx)
	if err != nil {
		return auto, nil, nil, nil, err
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT id,generation,logical_model,provider_model,tier,capabilities,valid_from,valid_until,emergency_disabled
FROM model_catalog_entries
WHERE generation=$1
ORDER BY CASE tier WHEN 'economy' THEN 1 WHEN 'general' THEN 2 ELSE 3 END,logical_model`, auto.ConfigVersion)
	if err != nil {
		return auto, nil, nil, nil, err
	}
	catalog := make([]service.ModelCatalogEntry, 0)
	for rows.Next() {
		var entry service.ModelCatalogEntry
		var capabilities []byte
		var validUntil sql.NullTime
		if err := rows.Scan(&entry.ID, &entry.Generation, &entry.LogicalModel, &entry.ProviderModel, &entry.Tier, &capabilities, &entry.ValidFrom, &validUntil, &entry.EmergencyDisabled); err != nil {
			_ = rows.Close()
			return auto, nil, nil, nil, err
		}
		if err := json.Unmarshal(capabilities, &entry.Capabilities); err != nil {
			_ = rows.Close()
			return auto, nil, nil, nil, err
		}
		if validUntil.Valid {
			entry.ValidUntil = &validUntil.Time
		}
		catalog = append(catalog, entry)
	}
	if err := rows.Close(); err != nil {
		return auto, nil, nil, nil, err
	}

	poolRows, err := r.db.QueryContext(ctx, `
SELECT p.id,p.generation,p.tier,p.position,p.catalog_entry_id,e.logical_model,p.valid_from,p.valid_until
FROM auto_candidate_pools p
JOIN model_catalog_entries e ON e.id=p.catalog_entry_id
WHERE p.generation=$1
ORDER BY CASE p.tier WHEN 'economy' THEN 1 WHEN 'general' THEN 2 ELSE 3 END,p.position`, auto.ConfigVersion)
	if err != nil {
		return auto, catalog, nil, nil, err
	}
	pools := make([]service.AutoCandidate, 0)
	for poolRows.Next() {
		var item service.AutoCandidate
		var validUntil sql.NullTime
		if err := poolRows.Scan(&item.ID, &item.Generation, &item.Tier, &item.Position, &item.CatalogEntryID, &item.LogicalModel, &item.ValidFrom, &validUntil); err != nil {
			_ = poolRows.Close()
			return auto, catalog, nil, nil, err
		}
		if validUntil.Valid {
			item.ValidUntil = &validUntil.Time
		}
		pools = append(pools, item)
	}
	if err := poolRows.Close(); err != nil {
		return auto, catalog, nil, nil, err
	}

	if sessionLimit <= 0 || sessionLimit > 200 {
		sessionLimit = 50
	}
	sessionRows, err := r.db.QueryContext(ctx, `
SELECT id,tenant_id,employee_user_id,profile_version,signal_source,signal_status,
       hmac_key_version,reliability,routing_mode,config_version,analysis_eligible,
       quota_grace_eligible,status,selected_logical_model,selected_tier,selected_complexity,
       required_capabilities,routing_version,first_gateway_request_id,last_gateway_request_id,
       created_at,last_activity_at
FROM work_sessions
ORDER BY last_activity_at DESC,id DESC
LIMIT $1`, sessionLimit)
	if err != nil {
		return auto, catalog, pools, nil, err
	}
	defer sessionRows.Close()
	sessions := make([]service.WorkSessionRecord, 0, sessionLimit)
	for sessionRows.Next() {
		record, scanErr := scanWorkSession(sessionRows)
		if scanErr != nil {
			return auto, catalog, pools, nil, scanErr
		}
		sessions = append(sessions, record)
	}
	return auto, catalog, pools, sessions, sessionRows.Err()
}

func (r *workSessionRepository) ListConfigVersions(ctx context.Context, currentVersion int64) ([]service.WorkSessionConfigVersion, error) {
	rows, err := r.db.QueryContext(ctx, `
WITH versions AS (
    SELECT generation AS config_version FROM model_catalog_entries
    UNION
    SELECT generation AS config_version FROM auto_candidate_pools
    UNION
    SELECT config_version FROM work_sessions
    UNION
    SELECT $1::BIGINT AS config_version
), catalog_stats AS (
    SELECT generation,COUNT(*) AS model_count,MIN(created_at) AS created_at
    FROM model_catalog_entries
    GROUP BY generation
), candidate_stats AS (
    SELECT generation,COUNT(*) AS candidate_count,MIN(created_at) AS created_at
    FROM auto_candidate_pools
    GROUP BY generation
), session_stats AS (
    SELECT config_version,
           COUNT(*) AS session_count,
           COUNT(*) FILTER (WHERE reliability='reliable') AS reliable_session_count,
           COUNT(*) FILTER (WHERE reliability='unreliable') AS request_scoped_session_count,
           MIN(created_at) AS created_at
    FROM work_sessions
    GROUP BY config_version
)
SELECT v.config_version,
       v.config_version=$1,
       COALESCE(s.session_count,0),
       COALESCE(s.reliable_session_count,0),
       COALESCE(s.request_scoped_session_count,0),
       COALESCE(c.model_count,0),
       COALESCE(p.candidate_count,0),
       COALESCE(c.created_at,p.created_at,s.created_at)
FROM versions v
LEFT JOIN catalog_stats c ON c.generation=v.config_version
LEFT JOIN candidate_stats p ON p.generation=v.config_version
LEFT JOIN session_stats s ON s.config_version=v.config_version
ORDER BY v.config_version DESC`, currentVersion)
	if err != nil {
		return nil, err
	}
	versions := make([]service.WorkSessionConfigVersion, 0)
	versionIndex := make(map[int64]int)
	for rows.Next() {
		var item service.WorkSessionConfigVersion
		var createdAt sql.NullTime
		if err := rows.Scan(
			&item.ConfigVersion, &item.Current, &item.SessionCount,
			&item.ReliableSessionCount, &item.RequestScopedSessionCount,
			&item.ModelCount, &item.CandidateCount, &createdAt,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if createdAt.Valid {
			value := createdAt.Time
			item.CreatedAt = &value
		}
		item.Catalog = make([]service.ModelCatalogEntry, 0, item.ModelCount)
		item.CandidatePools = make([]service.AutoCandidate, 0, item.CandidateCount)
		versionIndex[item.ConfigVersion] = len(versions)
		versions = append(versions, item)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	catalogRows, err := r.db.QueryContext(ctx, `
SELECT id,generation,logical_model,provider_model,tier,capabilities,valid_from,valid_until,emergency_disabled
FROM model_catalog_entries
ORDER BY generation DESC,CASE tier WHEN 'economy' THEN 1 WHEN 'general' THEN 2 ELSE 3 END,logical_model`)
	if err != nil {
		return nil, err
	}
	for catalogRows.Next() {
		var entry service.ModelCatalogEntry
		var capabilities []byte
		var validUntil sql.NullTime
		if err := catalogRows.Scan(&entry.ID, &entry.Generation, &entry.LogicalModel, &entry.ProviderModel, &entry.Tier, &capabilities, &entry.ValidFrom, &validUntil, &entry.EmergencyDisabled); err != nil {
			_ = catalogRows.Close()
			return nil, err
		}
		if err := json.Unmarshal(capabilities, &entry.Capabilities); err != nil {
			_ = catalogRows.Close()
			return nil, err
		}
		if validUntil.Valid {
			entry.ValidUntil = &validUntil.Time
		}
		if index, ok := versionIndex[entry.Generation]; ok {
			versions[index].Catalog = append(versions[index].Catalog, entry)
		}
	}
	if err := catalogRows.Close(); err != nil {
		return nil, err
	}
	if err := catalogRows.Err(); err != nil {
		return nil, err
	}

	poolRows, err := r.db.QueryContext(ctx, `
SELECT p.id,p.generation,p.tier,p.position,p.catalog_entry_id,e.logical_model,p.valid_from,p.valid_until
FROM auto_candidate_pools p
JOIN model_catalog_entries e ON e.id=p.catalog_entry_id
ORDER BY p.generation DESC,CASE p.tier WHEN 'economy' THEN 1 WHEN 'general' THEN 2 ELSE 3 END,p.position`)
	if err != nil {
		return nil, err
	}
	for poolRows.Next() {
		var item service.AutoCandidate
		var validUntil sql.NullTime
		if err := poolRows.Scan(&item.ID, &item.Generation, &item.Tier, &item.Position, &item.CatalogEntryID, &item.LogicalModel, &item.ValidFrom, &validUntil); err != nil {
			_ = poolRows.Close()
			return nil, err
		}
		if validUntil.Valid {
			item.ValidUntil = &validUntil.Time
		}
		if index, ok := versionIndex[item.Generation]; ok {
			versions[index].CandidatePools = append(versions[index].CandidatePools, item)
		}
	}
	if err := poolRows.Close(); err != nil {
		return nil, err
	}
	if err := poolRows.Err(); err != nil {
		return nil, err
	}
	return versions, nil
}

func (r *workSessionRepository) SetEmergencyDisabled(ctx context.Context, logicalModel string, disabled bool, now time.Time) error {
	result, err := r.db.ExecContext(ctx, `
UPDATE model_catalog_entries
SET emergency_disabled=$2,updated_at=$3
WHERE logical_model=$1 AND emergency_disabled IS DISTINCT FROM $2`, logicalModel, disabled, now)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		var exists bool
		if err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM model_catalog_entries WHERE logical_model=$1)`, logicalModel).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return service.ErrModelCatalogNotFound
		}
	}
	return nil
}

func (r *workSessionRepository) IsModelAvailableForSession(ctx context.Context, sessionID uuid.UUID, logicalModel string, at time.Time) (bool, error) {
	var available bool
	err := r.db.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM work_sessions s
    JOIN model_catalog_entries e
      ON e.generation=s.config_version AND e.logical_model=$2
    WHERE s.id=$1
      AND e.valid_from <= $3
      AND (e.valid_until IS NULL OR e.valid_until > $3)
      AND NOT EXISTS (
          SELECT 1 FROM model_catalog_entries emergency
          WHERE emergency.logical_model=$2 AND emergency.emergency_disabled
      )
)`, sessionID, logicalModel, at).Scan(&available)
	return available, err
}
