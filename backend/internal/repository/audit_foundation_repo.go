package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type auditFoundationRepository struct {
	db *sql.DB
}

func NewAuditFoundationRepository(db *sql.DB) service.AuditFoundationRepository {
	return &auditFoundationRepository{db: db}
}

func (r *auditFoundationRepository) CheckFoundation(ctx context.Context) error {
	if r == nil || r.db == nil {
		return errors.New("audit database unavailable")
	}
	var ready bool
	err := r.db.QueryRowContext(ctx, `
SELECT
    EXISTS (SELECT 1 FROM schema_migrations WHERE filename = '174_core_audit_foundation.sql')
    AND to_regclass('public.audit_interactions') IS NOT NULL
    AND to_regclass('public.audit_content_parts') IS NOT NULL
    AND EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = 'public' AND table_name = 'usage_logs' AND column_name = 'gateway_request_id'
    )
    AND EXISTS (
        SELECT 1 FROM pg_indexes
        WHERE schemaname = 'public' AND indexname = 'usage_logs_gateway_request_id_idx'
    )
    AND EXISTS (
        SELECT 1 FROM pg_trigger
        WHERE tgname = 'audit_interactions_state_guard' AND NOT tgisinternal
    )`).Scan(&ready)
	if err != nil {
		return fmt.Errorf("audit database preflight: %w", err)
	}
	if !ready {
		return service.ErrAuditSchemaNotReady
	}
	return nil
}

func (r *auditFoundationRepository) CreateInteraction(ctx context.Context, record service.AuditInteractionRecord) error {
	_, err := r.db.ExecContext(ctx, `
INSERT INTO audit_interactions (
    id, gateway_request_id, subject_user_id, subject_email_snapshot,
    api_key_id, api_key_fingerprint, profile_version, protocol, endpoint,
    method, transport, requested_model, admitted_at, expires_at, last_activity_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		record.ID, record.GatewayRequestID, record.SubjectUserID, record.SubjectEmailSnapshot,
		record.APIKeyID, record.APIKeyFingerprint, record.ProfileVersion, record.Protocol,
		record.Endpoint, record.Method, record.Transport, record.RequestedModel,
		record.AdmittedAt, record.ExpiresAt, record.LastActivityAt,
	)
	if err != nil {
		return fmt.Errorf("create audit interaction: %w", err)
	}
	return nil
}

func (r *auditFoundationRepository) AdmitRequest(ctx context.Context, interaction service.AuditInteractionRecord, part service.AuditContentPartRecord) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin audit admission transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `
INSERT INTO audit_interactions (
    id, gateway_request_id, subject_user_id, subject_email_snapshot,
    api_key_id, api_key_fingerprint, profile_version, protocol, endpoint,
    method, transport, requested_model, admitted_at, expires_at, last_activity_at,
    request_sha256, request_part_count
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		interaction.ID, interaction.GatewayRequestID, interaction.SubjectUserID, interaction.SubjectEmailSnapshot,
		interaction.APIKeyID, interaction.APIKeyFingerprint, interaction.ProfileVersion, interaction.Protocol,
		interaction.Endpoint, interaction.Method, interaction.Transport, interaction.RequestedModel,
		interaction.AdmittedAt, interaction.ExpiresAt, interaction.LastActivityAt,
		interaction.RequestSHA256, interaction.RequestPartCount)
	if err != nil {
		return fmt.Errorf("insert audit admission interaction: %w", err)
	}
	p := part.Encrypted
	_, err = tx.ExecContext(ctx, `
INSERT INTO audit_content_parts (
    id, interaction_id, direction, sequence, nonce, ciphertext, auth_tag,
    key_version, aad_format_version, plaintext_length, ciphertext_length,
    plaintext_sha256, ciphertext_sha256, downstream_write_result, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		part.ID, part.InteractionID, part.Direction, part.Sequence, p.Nonce, p.Ciphertext,
		p.AuthTag, p.KeyVersion, p.AADFormatVersion, p.PlaintextLength, p.CiphertextLength,
		p.PlaintextSHA256, p.CiphertextSHA256, part.DownstreamWriteResult, part.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert audit admission content part: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit audit admission transaction: %w", err)
	}
	return nil
}

func (r *auditFoundationRepository) AppendEncryptedPart(ctx context.Context, record service.AuditContentPartRecord) error {
	writeResult := record.DownstreamWriteResult
	if writeResult == "" {
		writeResult = "not_applicable"
	}
	createdAt := record.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	part := record.Encrypted
	_, err := r.db.ExecContext(ctx, `
INSERT INTO audit_content_parts (
    id, interaction_id, direction, sequence, nonce, ciphertext, auth_tag,
    key_version, aad_format_version, plaintext_length, ciphertext_length,
    plaintext_sha256, ciphertext_sha256, downstream_write_result, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		record.ID, record.InteractionID, record.Direction, record.Sequence,
		part.Nonce, part.Ciphertext, part.AuthTag, part.KeyVersion, part.AADFormatVersion,
		part.PlaintextLength, part.CiphertextLength, part.PlaintextSHA256,
		part.CiphertextSHA256, writeResult, createdAt,
	)
	if err != nil {
		return fmt.Errorf("append encrypted audit part: %w", err)
	}
	return nil
}

func (r *auditFoundationRepository) CommitResponsePart(ctx context.Context, commit service.AuditResponsePartCommit) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin audit response part transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	part := commit.Part.Encrypted
	_, err = tx.ExecContext(ctx, `
INSERT INTO audit_content_parts (
    id, interaction_id, direction, sequence, nonce, ciphertext, auth_tag,
    key_version, aad_format_version, plaintext_length, ciphertext_length,
    plaintext_sha256, ciphertext_sha256, downstream_write_result, created_at
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
		commit.Part.ID, commit.Part.InteractionID, commit.Part.Direction, commit.Part.Sequence,
		part.Nonce, part.Ciphertext, part.AuthTag, part.KeyVersion, part.AADFormatVersion,
		part.PlaintextLength, part.CiphertextLength, part.PlaintextSHA256,
		part.CiphertextSHA256, "pending", commit.Part.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert audit response part: %w", err)
	}
	at := commit.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	result, err := tx.ExecContext(ctx, `
UPDATE audit_interactions
SET response_sha256 = $1,
    response_part_count = response_part_count + 1,
    downstream_status = COALESCE(downstream_status, $2),
    downstream_write_result = 'pending',
    last_activity_at = $3
WHERE id = $4
  AND content_state = 'recording'
  AND response_part_count = $5
  AND (downstream_status IS NULL OR downstream_status = $2)`,
		commit.ResponseSHA256, commit.DownstreamStatus, at,
		commit.Part.InteractionID, commit.ExpectedPartCount)
	if err != nil {
		return fmt.Errorf("update audit response progress: %w", err)
	}
	applied, err := oneRowChanged(result)
	if err != nil {
		return fmt.Errorf("read audit response progress result: %w", err)
	}
	if !applied {
		return service.ErrAuditCASConflict
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit audit response part transaction: %w", err)
	}
	return nil
}

func (r *auditFoundationRepository) SetResponseWriteResult(ctx context.Context, update service.AuditResponseWriteResult) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin audit downstream result transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `
UPDATE audit_content_parts
SET downstream_write_result = $1
WHERE id = $2 AND interaction_id = $3 AND direction = 'response'
  AND sequence = $4 AND downstream_write_result = 'pending'`,
		update.Result, update.PartID, update.InteractionID, update.Sequence)
	if err != nil {
		return fmt.Errorf("update audit response part write result: %w", err)
	}
	applied, err := oneRowChanged(result)
	if err != nil {
		return fmt.Errorf("read audit response part write result: %w", err)
	}
	if !applied {
		return service.ErrAuditCASConflict
	}
	at := update.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	result, err = tx.ExecContext(ctx, `
UPDATE audit_interactions
SET downstream_write_result = $1, last_activity_at = $2
WHERE id = $3 AND content_state = 'recording'`, update.Result, at, update.InteractionID)
	if err != nil {
		return fmt.Errorf("update audit interaction write result: %w", err)
	}
	applied, err = oneRowChanged(result)
	if err != nil {
		return fmt.Errorf("read audit interaction write result: %w", err)
	}
	if !applied {
		return service.ErrAuditCASConflict
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit audit downstream result transaction: %w", err)
	}
	return nil
}

func (r *auditFoundationRepository) FinalizeInteraction(ctx context.Context, final service.AuditInteractionFinalization) error {
	at := final.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	result, err := r.db.ExecContext(ctx, `
UPDATE audit_interactions
SET request_outcome = $1,
    request_outcome_version = request_outcome_version + 1,
    content_state = $2,
    content_state_version = content_state_version + 1,
    downstream_write_result = $3,
    completed_at = COALESCE(completed_at, $4),
    last_activity_at = $4,
    safe_error_summary = COALESCE($5, safe_error_summary)
WHERE id = $6
  AND request_outcome = 'processing' AND request_outcome_version = 0
  AND content_state = 'recording' AND content_state_version = 0`,
		final.RequestOutcome, final.ContentState, final.WriteResult, at,
		final.SafeErrorSummary, final.InteractionID)
	if err != nil {
		return fmt.Errorf("finalize audit interaction: %w", err)
	}
	applied, err := oneRowChanged(result)
	if err != nil {
		return fmt.Errorf("read audit interaction finalization result: %w", err)
	}
	if !applied {
		return service.ErrAuditCASConflict
	}
	return nil
}

func (r *auditFoundationRepository) CompareAndSwapRequestOutcome(ctx context.Context, change service.AuditStateCAS) (bool, error) {
	at := change.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	result, err := r.db.ExecContext(ctx, `
UPDATE audit_interactions
SET request_outcome = $1,
    request_outcome_version = request_outcome_version + 1,
    completed_at = COALESCE(completed_at, $2),
    last_activity_at = $2,
    safe_error_summary = COALESCE($3, safe_error_summary)
WHERE id = $4 AND request_outcome = $5 AND request_outcome_version = $6`,
		change.NextState, at, change.SafeErrorSummary, change.InteractionID,
		change.ExpectedState, change.ExpectedVersion,
	)
	if err != nil {
		return false, fmt.Errorf("compare and swap audit request outcome: %w", err)
	}
	return oneRowChanged(result)
}

func (r *auditFoundationRepository) CompareAndSwapContentState(ctx context.Context, change service.AuditStateCAS) (bool, error) {
	at := change.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	result, err := r.db.ExecContext(ctx, `
UPDATE audit_interactions
SET content_state = $1,
    content_state_version = content_state_version + 1,
    last_activity_at = $2,
    safe_error_summary = COALESCE($3, safe_error_summary)
WHERE id = $4 AND content_state = $5 AND content_state_version = $6`,
		change.NextState, at, change.SafeErrorSummary, change.InteractionID,
		change.ExpectedState, change.ExpectedVersion,
	)
	if err != nil {
		return false, fmt.Errorf("compare and swap audit content state: %w", err)
	}
	return oneRowChanged(result)
}

func (r *auditFoundationRepository) ReconcileStale(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := r.db.ExecContext(ctx, `
UPDATE audit_interactions
SET request_outcome = CASE WHEN request_outcome = 'processing' THEN 'interrupted' ELSE request_outcome END,
    request_outcome_version = request_outcome_version + CASE WHEN request_outcome = 'processing' THEN 1 ELSE 0 END,
    content_state = CASE WHEN content_state = 'recording' THEN 'incomplete' ELSE content_state END,
    content_state_version = content_state_version + CASE WHEN content_state = 'recording' THEN 1 ELSE 0 END,
    completed_at = CASE WHEN request_outcome = 'processing' THEN COALESCE(completed_at, NOW()) ELSE completed_at END,
    last_activity_at = NOW(),
    safe_error_summary = COALESCE(safe_error_summary, 'stale_interaction_reconciled')
WHERE last_activity_at < $1
  AND (request_outcome = 'processing' OR content_state = 'recording')`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("reconcile stale audit interactions: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("read audit reconciliation result: %w", err)
	}
	return count, nil
}

func oneRowChanged(result sql.Result) (bool, error) {
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return count == 1, nil
}
