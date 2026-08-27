package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
)

type auditManagementRepository struct{ db *sql.DB }

func NewAuditManagementRepository(db *sql.DB) service.AuditManagementRepository {
	return &auditManagementRepository{db: db}
}

func (r *auditManagementRepository) ListAuditMetadata(ctx context.Context, filter service.AuditMetadataFilter) (service.AuditMetadataPage, error) {
	where := []string{"1=1"}
	args := make([]any, 0, 8)
	add := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	if filter.Employee != "" {
		placeholder := add(filter.Employee)
		if _, err := strconv.ParseInt(filter.Employee, 10, 64); err == nil {
			where = append(where, "(subject_user_id::text = "+placeholder+" OR subject_email_snapshot ILIKE '%' || "+placeholder+" || '%')")
		} else {
			where = append(where, "subject_email_snapshot ILIKE '%' || "+placeholder+" || '%'")
		}
	}
	if filter.From != nil {
		where = append(where, "admitted_at >= "+add(filter.From.UTC()))
	}
	if filter.To != nil {
		where = append(where, "admitted_at <= "+add(filter.To.UTC()))
	}
	if filter.Protocol != "" {
		where = append(where, "protocol = "+add(filter.Protocol))
	}
	if filter.Model != "" {
		placeholder := add(filter.Model)
		where = append(where, "(requested_model = "+placeholder+" OR resolved_model = "+placeholder+")")
	}
	if filter.RequestOutcome != "" {
		where = append(where, "request_outcome = "+add(filter.RequestOutcome))
	}
	if filter.ContentState != "" {
		where = append(where, "content_state = "+add(filter.ContentState))
	}
	if filter.GatewayRequestID != nil {
		where = append(where, "gateway_request_id = "+add(*filter.GatewayRequestID))
	}
	whereSQL := strings.Join(where, " AND ")

	var total int64
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM audit_interactions WHERE "+whereSQL, args...).Scan(&total); err != nil {
		return service.AuditMetadataPage{}, fmt.Errorf("count audit metadata: %w", err)
	}
	pageArgs := append([]any(nil), args...)
	limitPlaceholder := "$" + strconv.Itoa(len(pageArgs)+1)
	pageArgs = append(pageArgs, filter.PageSize)
	offsetPlaceholder := "$" + strconv.Itoa(len(pageArgs)+1)
	pageArgs = append(pageArgs, (filter.Page-1)*filter.PageSize)
	rows, err := r.db.QueryContext(ctx, `
SELECT id,gateway_request_id,subject_user_id,subject_email_snapshot,
       profile_version,protocol,endpoint,method,transport,requested_model,resolved_model,
       request_outcome,content_state,downstream_status,downstream_write_result,
       admitted_at,completed_at,expires_at,last_activity_at,
       request_part_count,response_part_count,safe_error_summary
FROM audit_interactions
WHERE `+whereSQL+`
ORDER BY admitted_at DESC,id DESC
LIMIT `+limitPlaceholder+` OFFSET `+offsetPlaceholder, pageArgs...)
	if err != nil {
		return service.AuditMetadataPage{}, fmt.Errorf("query audit metadata: %w", err)
	}
	defer rows.Close()
	items := make([]service.AuditMetadataRecord, 0, filter.PageSize)
	for rows.Next() {
		record, scanErr := scanAuditMetadata(rows)
		if scanErr != nil {
			return service.AuditMetadataPage{}, fmt.Errorf("scan audit metadata: %w", scanErr)
		}
		items = append(items, record)
	}
	if err := rows.Err(); err != nil {
		return service.AuditMetadataPage{}, fmt.Errorf("iterate audit metadata: %w", err)
	}
	return service.AuditMetadataPage{Items: items, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

type auditRowScanner interface{ Scan(...any) error }

func scanAuditMetadata(scanner auditRowScanner) (service.AuditMetadataRecord, error) {
	var record service.AuditMetadataRecord
	var subjectUserID sql.NullInt64
	var subjectEmail, requestedModel, resolvedModel, safeError sql.NullString
	var downstreamStatus sql.NullInt64
	var completedAt sql.NullTime
	err := scanner.Scan(
		&record.ID, &record.GatewayRequestID, &subjectUserID, &subjectEmail,
		&record.ProfileVersion, &record.Protocol, &record.Endpoint, &record.Method, &record.Transport,
		&requestedModel, &resolvedModel, &record.RequestOutcome, &record.ContentState,
		&downstreamStatus, &record.DownstreamWriteResult, &record.AdmittedAt, &completedAt,
		&record.ExpiresAt, &record.LastActivityAt, &record.RequestPartCount,
		&record.ResponsePartCount, &safeError,
	)
	if err != nil {
		return record, err
	}
	if subjectUserID.Valid {
		value := subjectUserID.Int64
		record.SubjectUserID = &value
	}
	if subjectEmail.Valid {
		value := subjectEmail.String
		record.SubjectEmailSnapshot = &value
	}
	if requestedModel.Valid {
		value := requestedModel.String
		record.RequestedModel = &value
	}
	if resolvedModel.Valid {
		value := resolvedModel.String
		record.ResolvedModel = &value
	}
	if downstreamStatus.Valid {
		value := int(downstreamStatus.Int64)
		record.DownstreamStatus = &value
	}
	if completedAt.Valid {
		value := completedAt.Time
		record.CompletedAt = &value
	}
	if safeError.Valid {
		value := safeError.String
		record.SafeErrorSummary = &value
	}
	return record, nil
}

type lockedDisclosureActor struct {
	seatUserID     int64
	role           string
	status         string
	deleted        bool
	sessionVersion int64
}

func lockDisclosureActor(ctx context.Context, tx *sql.Tx) (lockedDisclosureActor, error) {
	var actor lockedDisclosureActor
	var deletedAt sql.NullTime
	err := tx.QueryRowContext(ctx, `
SELECT s.user_id,u.role,u.status,u.deleted_at,u.session_version
FROM super_admin_seat s
JOIN users u ON u.id=s.user_id
WHERE s.singleton_id=1
FOR UPDATE OF s,u`).Scan(&actor.seatUserID, &actor.role, &actor.status, &deletedAt, &actor.sessionVersion)
	actor.deleted = deletedAt.Valid
	return actor, err
}

func disclosureActorAllowed(locked lockedDisclosureActor, actor service.AuditDisclosureActor) bool {
	return actor.UserID == locked.seatUserID && actor.SessionVersion == locked.sessionVersion &&
		!actor.SessionExpiresAt.IsZero() && actor.SessionExpiresAt.After(time.Now().UTC()) &&
		actor.Role == service.RoleSuperAdmin && actor.AuthMethod == "jwt" &&
		locked.role == service.RoleSuperAdmin && locked.status == service.StatusActive && !locked.deleted
}

func (r *auditManagementRepository) RecordDisclosureStarted(ctx context.Context, operationID uuid.UUID, actor service.AuditDisclosureActor, interactionID uuid.UUID) error {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin audit disclosure start: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	locked, err := lockDisclosureActor(ctx, tx)
	if err != nil {
		return fmt.Errorf("lock audit disclosure actor: %w", err)
	}
	if !disclosureActorAllowed(locked, actor) {
		if actor.UserID > 0 {
			if eventErr := insertDisclosureEvent(ctx, tx, operationID, 1, actor.UserID, interactionID, "rejected", "", "named_super_admin_session_required"); eventErr != nil {
				return fmt.Errorf("record audit disclosure rejection: %w", eventErr)
			}
			if commitErr := tx.Commit(); commitErr != nil {
				return fmt.Errorf("commit audit disclosure rejection: %w", commitErr)
			}
		}
		return service.ErrAuditDisclosureForbidden
	}
	if err := insertDisclosureEvent(ctx, tx, operationID, 1, actor.UserID, interactionID, "started", "", ""); err != nil {
		return fmt.Errorf("record audit disclosure start: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit audit disclosure start: %w", err)
	}
	return nil
}

func (r *auditManagementRepository) LoadDisclosureMaterial(ctx context.Context, interactionID uuid.UUID) (service.AuditDisclosureMaterial, error) {
	row := r.db.QueryRowContext(ctx, `
SELECT id,gateway_request_id,subject_user_id,subject_email_snapshot,
       profile_version,protocol,endpoint,method,transport,requested_model,resolved_model,
       request_outcome,content_state,downstream_status,downstream_write_result,
       admitted_at,completed_at,expires_at,last_activity_at,
       request_part_count,response_part_count,safe_error_summary
FROM audit_interactions
WHERE id=$1 AND expires_at > NOW() AND content_state IN ('complete','incomplete')`, interactionID)
	metadata, err := scanAuditMetadata(row)
	if errors.Is(err, sql.ErrNoRows) {
		return service.AuditDisclosureMaterial{}, service.ErrAuditContentUnavailable
	}
	if err != nil {
		return service.AuditDisclosureMaterial{}, fmt.Errorf("load audit disclosure metadata: %w", err)
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT direction,sequence,nonce,ciphertext,auth_tag,key_version,aad_format_version,
       plaintext_length,ciphertext_length,plaintext_sha256,ciphertext_sha256
FROM audit_content_parts
WHERE interaction_id=$1
ORDER BY CASE direction WHEN 'request' THEN 0 ELSE 1 END,sequence`, interactionID)
	if err != nil {
		return service.AuditDisclosureMaterial{}, fmt.Errorf("load audit disclosure content: %w", err)
	}
	defer rows.Close()
	parts := make([]service.AuditDisclosureMaterialPart, 0, metadata.RequestPartCount+metadata.ResponsePartCount)
	for rows.Next() {
		var part service.AuditDisclosureMaterialPart
		if err := rows.Scan(
			&part.Direction, &part.Sequence, &part.Encrypted.Nonce, &part.Encrypted.Ciphertext,
			&part.Encrypted.AuthTag, &part.Encrypted.KeyVersion, &part.Encrypted.AADFormatVersion,
			&part.Encrypted.PlaintextLength, &part.Encrypted.CiphertextLength,
			&part.Encrypted.PlaintextSHA256, &part.Encrypted.CiphertextSHA256,
		); err != nil {
			return service.AuditDisclosureMaterial{}, fmt.Errorf("scan audit disclosure content: %w", err)
		}
		parts = append(parts, part)
	}
	if err := rows.Err(); err != nil {
		return service.AuditDisclosureMaterial{}, fmt.Errorf("iterate audit disclosure content: %w", err)
	}
	if len(parts) == 0 || len(parts) != metadata.RequestPartCount+metadata.ResponsePartCount {
		return service.AuditDisclosureMaterial{}, service.ErrAuditContentUnavailable
	}
	return service.AuditDisclosureMaterial{Metadata: metadata, Parts: parts}, nil
}

func (r *auditManagementRepository) RecordDisclosureCompleted(ctx context.Context, operationID uuid.UUID, actor service.AuditDisclosureActor, interactionID uuid.UUID, succeeded bool, safeSummary string) error {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return fmt.Errorf("begin audit disclosure completion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	locked, err := lockDisclosureActor(ctx, tx)
	if err != nil {
		return fmt.Errorf("lock audit disclosure completion actor: %w", err)
	}
	var startedActorID int64
	if err := tx.QueryRowContext(ctx, `
SELECT actor_user_id
FROM governance_events
WHERE operation_id=$1 AND event_sequence=1 AND action='raw_content_disclosure' AND result='started'
FOR SHARE`, operationID).Scan(&startedActorID); err != nil || startedActorID != actor.UserID {
		if err == nil {
			err = service.ErrAuditGovernanceUnavailable
		}
		return fmt.Errorf("verify audit disclosure start: %w", err)
	}
	allowed := disclosureActorAllowed(locked, actor)
	result := "failed"
	if succeeded && allowed {
		var available bool
		availabilityErr := tx.QueryRowContext(ctx, `
SELECT expires_at > NOW() AND content_state IN ('complete','incomplete')
FROM audit_interactions WHERE id=$1`, interactionID).Scan(&available)
		if availabilityErr != nil && !errors.Is(availabilityErr, sql.ErrNoRows) {
			return fmt.Errorf("verify audit disclosure retention: %w", availabilityErr)
		}
		if !available {
			safeSummary = "content_expired_during_disclosure"
		}
		if available {
			result = "succeeded"
		}
	}
	if !allowed {
		safeSummary = "session_invalidated_during_disclosure"
	}
	if safeSummary == "" && result == "failed" {
		safeSummary = "content_disclosure_failed"
	}
	if err := insertDisclosureEvent(ctx, tx, operationID, 2, actor.UserID, interactionID, result, "", safeSummary); err != nil {
		return fmt.Errorf("record audit disclosure completion: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit audit disclosure completion: %w", err)
	}
	if !allowed {
		return service.ErrAuditDisclosureForbidden
	}
	if succeeded && result != "succeeded" {
		return service.ErrAuditContentUnavailable
	}
	return nil
}

func insertDisclosureEvent(ctx context.Context, tx *sql.Tx, operationID uuid.UUID, sequence int, actorUserID int64, interactionID uuid.UUID, result, reason, safeSummary string) error {
	var gatewayRequestID sql.NullString
	_ = tx.QueryRowContext(ctx, `SELECT gateway_request_id::text FROM audit_interactions WHERE id=$1`, interactionID).Scan(&gatewayRequestID)
	_, err := tx.ExecContext(ctx, `
INSERT INTO governance_events (
 id,operation_id,event_sequence,actor_kind,actor_user_id,target_kind,target_id,
 action,result,reason,gateway_request_id,safe_error_summary,occurred_at
) VALUES ($1,$2,$3,'named_admin',$4,'audit_interaction',$5,
          'raw_content_disclosure',$6,NULLIF($7,''),$8,NULLIF($9,''),NOW())`,
		uuid.New(), operationID, sequence, actorUserID, interactionID.String(), result,
		reason, nullableString(gatewayRequestID), safeSummary)
	return err
}

func nullableString(value sql.NullString) any {
	if !value.Valid {
		return nil
	}
	return value.String
}

func (r *auditManagementRepository) PurgeExpiredAuditContent(ctx context.Context, cutoff time.Time, batchSize int) (service.AuditRetentionResult, error) {
	if batchSize <= 0 {
		batchSize = 100
	}
	rows, err := r.db.QueryContext(ctx, `
SELECT i.id
FROM audit_interactions i
WHERE i.expires_at <= $1
  AND (i.content_state <> 'expired' OR EXISTS (
      SELECT 1 FROM audit_content_parts p WHERE p.interaction_id=i.id
  ))
ORDER BY i.expires_at,i.id
LIMIT $2`, cutoff, batchSize)
	if err != nil {
		return service.AuditRetentionResult{}, fmt.Errorf("query expired audit content: %w", err)
	}
	ids := make([]uuid.UUID, 0, batchSize)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return service.AuditRetentionResult{}, fmt.Errorf("scan expired audit interaction: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return service.AuditRetentionResult{}, fmt.Errorf("close expired audit query: %w", err)
	}
	if err := rows.Err(); err != nil {
		return service.AuditRetentionResult{}, fmt.Errorf("iterate expired audit interactions: %w", err)
	}
	result := service.AuditRetentionResult{Candidates: len(ids)}
	for _, id := range ids {
		if err := r.purgeExpiredInteraction(ctx, id, cutoff); err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			result.Failed++
			continue
		}
		result.Purged++
	}
	return result, nil
}

func (r *auditManagementRepository) purgeExpiredInteraction(ctx context.Context, interactionID uuid.UUID, cutoff time.Time) error {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var outcome, contentState string
	var expiresAt time.Time
	if err := tx.QueryRowContext(ctx, `
SELECT request_outcome,content_state,expires_at
FROM audit_interactions WHERE id=$1 FOR UPDATE`, interactionID).Scan(&outcome, &contentState, &expiresAt); err != nil {
		return err
	}
	if expiresAt.After(cutoff) {
		return nil
	}
	if outcome == service.AuditRequestProcessing || contentState == service.AuditContentRecording {
		if _, err := tx.ExecContext(ctx, `
UPDATE audit_interactions
SET request_outcome=CASE WHEN request_outcome='processing' THEN 'interrupted' ELSE request_outcome END,
    request_outcome_version=request_outcome_version+CASE WHEN request_outcome='processing' THEN 1 ELSE 0 END,
    content_state=CASE WHEN content_state='recording' THEN 'incomplete' ELSE content_state END,
    content_state_version=content_state_version+CASE WHEN content_state='recording' THEN 1 ELSE 0 END,
    completed_at=CASE WHEN request_outcome='processing' THEN COALESCE(completed_at,$2) ELSE completed_at END,
    last_activity_at=GREATEST(last_activity_at,$2),
    safe_error_summary=COALESCE(safe_error_summary,'retention_cleanup_reconciled')
WHERE id=$1`, interactionID, cutoff); err != nil {
			return err
		}
		if contentState == service.AuditContentRecording {
			contentState = service.AuditContentIncomplete
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM audit_content_parts WHERE interaction_id=$1`, interactionID); err != nil {
		return err
	}
	if contentState != service.AuditContentExpired {
		update, err := tx.ExecContext(ctx, `
UPDATE audit_interactions
SET content_state='expired',content_state_version=content_state_version+1,
    last_activity_at=GREATEST(last_activity_at,$2)
WHERE id=$1 AND content_state IN ('complete','incomplete')`, interactionID, cutoff)
		if err != nil {
			return err
		}
		changed, err := update.RowsAffected()
		if err != nil || changed != 1 {
			return service.ErrAuditCASConflict
		}
	}
	return tx.Commit()
}
