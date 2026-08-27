package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/lib/pq"
)

type governanceRepository struct{ db *sql.DB }

func NewGovernanceRepository(db *sql.DB) service.GovernanceRepository {
	return &governanceRepository{db: db}
}

func (r *governanceRepository) GetSuperAdminSeat(ctx context.Context) (*service.SuperAdminTransferResult, error) {
	var userID, version int64
	if err := r.db.QueryRowContext(ctx, `SELECT user_id,version FROM super_admin_seat WHERE singleton_id=1`).Scan(&userID, &version); err != nil {
		return nil, err
	}
	return &service.SuperAdminTransferResult{CurrentUserID: userID, SeatVersion: version}, nil
}

func (r *governanceRepository) IsCurrentSuperAdmin(ctx context.Context, userID int64) (bool, error) {
	var allowed bool
	err := r.db.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM super_admin_seat s
  JOIN users u ON u.id=s.user_id
  WHERE s.singleton_id=1 AND s.user_id=$1
    AND u.role='super_admin' AND u.status='active' AND u.deleted_at IS NULL
)`, userID).Scan(&allowed)
	return allowed, err
}

func (r *governanceRepository) RecordGovernanceRejection(ctx context.Context, in service.SuperAdminTransferInput, safeReason string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	actorKind := "system"
	var actor *int64
	if in.NamedActor {
		actorKind = "named_admin"
		actor = &in.ActorUserID
	}
	if err := insertGovernanceEvent(ctx, tx, in.OperationID, actorKind, actor, nil, in.TargetUserID, "transfer_super_admin", "rejected", in.Reason, nil, nil, nil, &safeReason); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *governanceRepository) RecordRecoveryRejection(ctx context.Context, in service.EmergencyRecoveryInput, safeReason string, attributed bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	actorKind := "system"
	var operatorID *string
	var nonceFingerprint *string
	if attributed {
		actorKind = "deployment_operator"
		operatorID = &in.DeploymentOperatorID
		if in.NonceFingerprint != "" {
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM governance_events WHERE recovery_nonce_fingerprint=$1)`, in.NonceFingerprint).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				nonceFingerprint = &in.NonceFingerprint
			}
		}
	}
	if err := insertGovernanceEvent(ctx, tx, in.OperationID, actorKind, nil, operatorID, in.TargetUserID, "emergency_recover_super_admin", "rejected", in.Reason, nil, nil, nonceFingerprint, &safeReason); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *governanceRepository) RecordUserLifecycleRejection(ctx context.Context, in service.UserLifecycleInput, safeReason string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	actorKind := "system"
	var actor *int64
	if in.NamedActor {
		actorKind = "named_admin"
		actor = &in.ActorUserID
	}
	if err := insertGovernanceEvent(ctx, tx, in.OperationID, actorKind, actor, nil, in.TargetUserID, in.Action, "rejected", in.Reason, nil, nil, nil, &safeReason); err != nil {
		return err
	}
	return tx.Commit()
}

type lockedSeat struct{ userID, version int64 }

type lockedGovernanceUser struct {
	role           string
	status         string
	deleted        bool
	sessionVersion int64
}

func lockSeat(ctx context.Context, tx *sql.Tx) (lockedSeat, error) {
	var seat lockedSeat
	err := tx.QueryRowContext(ctx, `SELECT user_id,version FROM super_admin_seat WHERE singleton_id=1 FOR UPDATE`).Scan(&seat.userID, &seat.version)
	if errors.Is(err, sql.ErrNoRows) {
		return seat, service.ErrSuperAdminConflict
	}
	return seat, err
}

func lockGovernanceUsers(ctx context.Context, tx *sql.Tx, firstID, secondID int64) (map[int64]lockedGovernanceUser, error) {
	ids := []int64{firstID}
	if secondID != firstID {
		ids = append(ids, secondID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	rows, err := tx.QueryContext(ctx, `
SELECT id,role,status,deleted_at,session_version
FROM users
WHERE id = ANY($1)
ORDER BY id
FOR UPDATE`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make(map[int64]lockedGovernanceUser, len(ids))
	for rows.Next() {
		var id int64
		var user lockedGovernanceUser
		var deleted sql.NullTime
		if err := rows.Scan(&id, &user.role, &user.status, &deleted, &user.sessionVersion); err != nil {
			return nil, err
		}
		user.deleted = deleted.Valid
		users[id] = user
	}
	return users, rows.Err()
}

func governanceSummary(role, status string, sessionVersion int64) []byte {
	raw, _ := json.Marshal(map[string]any{"role": role, "status": status, "session_version": sessionVersion})
	return raw
}

func insertGovernanceEvent(ctx context.Context, tx *sql.Tx, operationID uuid.UUID, actorKind string, actorUserID *int64, operatorID *string, targetID int64, action, result, reason string, before, after []byte, nonceFingerprint *string, safeError *string) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO governance_events (
 id, operation_id, event_sequence, actor_kind, actor_user_id, deployment_operator_id,
 target_kind, target_id, action, result, reason, before_summary, after_summary,
 recovery_nonce_fingerprint, safe_error_summary, occurred_at
) VALUES ($1,$2,1,$3,$4,$5,'user',$6,$7,$8,NULLIF($9,''),$10,$11,$12,$13,$14)`,
		uuid.New(), operationID, actorKind, actorUserID, operatorID, strconv.FormatInt(targetID, 10), action, result, reason,
		nullJSON(before), nullJSON(after), nonceFingerprint, safeError, time.Now().UTC())
	return err
}

func nullJSON(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return raw
}

func (r *governanceRepository) TransferSuperAdmin(ctx context.Context, in service.SuperAdminTransferInput) (*service.SuperAdminTransferResult, error) {
	return r.transfer(ctx, in.OperationID, "named_admin", &in.ActorUserID, nil, in.TargetUserID, in.ExpectedSeatVersion, in.Reason, nil)
}

func (r *governanceRepository) EmergencyRecoverSuperAdmin(ctx context.Context, in service.EmergencyRecoveryInput) (*service.SuperAdminTransferResult, error) {
	return r.transfer(ctx, in.OperationID, "deployment_operator", nil, &in.DeploymentOperatorID, in.TargetUserID, 0, in.Reason, &in.NonceFingerprint)
}

func (r *governanceRepository) transfer(ctx context.Context, operationID uuid.UUID, actorKind string, actorUserID *int64, operatorID *string, targetID, expectedVersion int64, reason string, nonceFingerprint *string) (*service.SuperAdminTransferResult, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	seat, err := lockSeat(ctx, tx)
	if err != nil {
		return nil, err
	}
	if nonceFingerprint != nil {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM governance_events WHERE recovery_nonce_fingerprint=$1)`, *nonceFingerprint).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			safe := "recovery_nonce_replay"
			if eventErr := insertGovernanceEvent(ctx, tx, operationID, actorKind, actorUserID, operatorID, targetID, actionForActor(actorKind), "rejected", reason, nil, nil, nil, &safe); eventErr != nil {
				return nil, eventErr
			}
			if commitErr := tx.Commit(); commitErr != nil {
				return nil, commitErr
			}
			return nil, service.ErrSuperAdminRecoveryInvalid
		}
	}
	reject := func(expected error, safe string) (*service.SuperAdminTransferResult, error) {
		if eventErr := insertGovernanceEvent(ctx, tx, operationID, actorKind, actorUserID, operatorID, targetID, actionForActor(actorKind), "rejected", reason, nil, nil, nonceFingerprint, &safe); eventErr != nil {
			return nil, eventErr
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return nil, commitErr
		}
		return nil, expected
	}
	if expectedVersion > 0 && expectedVersion != seat.version {
		return reject(service.ErrSuperAdminConflict, "seat_version_conflict")
	}
	if actorUserID != nil && *actorUserID != seat.userID {
		return reject(service.ErrSuperAdminForbidden, "actor_is_not_current_seat_holder")
	}
	if targetID <= 0 || targetID == seat.userID {
		return reject(service.ErrSuperAdminTargetInvalid, "invalid_or_self_target")
	}

	users, err := lockGovernanceUsers(ctx, tx, seat.userID, targetID)
	if err != nil {
		return nil, err
	}
	target, targetExists := users[targetID]
	if !targetExists {
		return reject(service.ErrSuperAdminTargetInvalid, "target_not_found")
	}
	if target.role != service.RoleAdmin || target.status != service.StatusActive || target.deleted {
		return reject(service.ErrSuperAdminTargetInvalid, "target_not_active_admin")
	}
	oldHolder, oldHolderExists := users[seat.userID]
	if !oldHolderExists || oldHolder.role != service.RoleSuperAdmin || oldHolder.status != service.StatusActive || oldHolder.deleted {
		return nil, service.ErrSuperAdminConflict
	}

	oldResult, err := tx.ExecContext(ctx, `UPDATE users SET role='admin',session_version=session_version+1,updated_at=NOW() WHERE id=$1 AND role='super_admin' AND status='active' AND deleted_at IS NULL`, seat.userID)
	if err != nil {
		return nil, err
	}
	newResult, err := tx.ExecContext(ctx, `UPDATE users SET role='super_admin',session_version=session_version+1,updated_at=NOW() WHERE id=$1 AND role='admin' AND status='active' AND deleted_at IS NULL`, targetID)
	if err != nil {
		return nil, err
	}
	if !exactlyOneRow(oldResult) || !exactlyOneRow(newResult) {
		return nil, service.ErrSuperAdminConflict
	}
	newVersion := seat.version + 1
	seatResult, err := tx.ExecContext(ctx, `UPDATE super_admin_seat SET user_id=$1,version=$2,updated_at=NOW() WHERE singleton_id=1 AND version=$3`, targetID, newVersion, seat.version)
	if err != nil {
		return nil, err
	}
	if !exactlyOneRow(seatResult) {
		return nil, service.ErrSuperAdminConflict
	}
	before := governanceSummary(service.RoleSuperAdmin, service.StatusActive, oldHolder.sessionVersion)
	after := governanceSummary(service.RoleSuperAdmin, service.StatusActive, target.sessionVersion+1)
	if err := insertGovernanceEvent(ctx, tx, operationID, actorKind, actorUserID, operatorID, targetID, actionForActor(actorKind), "succeeded", reason, before, after, nonceFingerprint, nil); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, governanceCommitError(err, service.ErrSuperAdminConflict)
	}
	return &service.SuperAdminTransferResult{PreviousUserID: seat.userID, CurrentUserID: targetID, SeatVersion: newVersion}, nil
}

func exactlyOneRow(result sql.Result) bool {
	if result == nil {
		return false
	}
	rows, err := result.RowsAffected()
	return err == nil && rows == 1
}

func actionForActor(actorKind string) string {
	if actorKind == "deployment_operator" {
		return "emergency_recover_super_admin"
	}
	return "transfer_super_admin"
}

func (r *governanceRepository) ChangeUserLifecycle(ctx context.Context, in service.UserLifecycleInput) error {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	seat, err := lockSeat(ctx, tx)
	if err != nil {
		return err
	}
	users, err := lockGovernanceUsers(ctx, tx, in.ActorUserID, in.TargetUserID)
	if err != nil {
		return err
	}
	actor, actorExists := users[in.ActorUserID]
	target, targetExists := users[in.TargetUserID]
	actorKind := "system"
	var actorID *int64
	if actorExists && (actor.role == service.RoleAdmin || actor.role == service.RoleSuperAdmin) {
		actorKind = "named_admin"
		actorID = &in.ActorUserID
	}
	reject := func(expected error, safe string) error {
		if eventErr := insertGovernanceEvent(ctx, tx, in.OperationID, actorKind, actorID, nil, in.TargetUserID, in.Action, "rejected", in.Reason, nil, nil, nil, &safe); eventErr != nil {
			return eventErr
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return governanceCommitError(commitErr, service.ErrUserLifecycleConflict)
		}
		return expected
	}
	if !actorExists || actor.deleted || actor.status != service.StatusActive || (actor.role != service.RoleAdmin && actor.role != service.RoleSuperAdmin) {
		return reject(service.ErrUserLifecycleForbidden, "actor_not_active_named_admin")
	}
	if actor.role == service.RoleSuperAdmin && in.ActorUserID != seat.userID {
		return reject(service.ErrUserLifecycleForbidden, "actor_is_not_current_seat_holder")
	}
	if !targetExists || target.deleted {
		return reject(service.ErrUserLifecycleInvalid, "target_not_found_or_deleted")
	}
	if in.TargetUserID == seat.userID || target.role == service.RoleSuperAdmin {
		return reject(service.ErrUserLifecycleForbidden, "target_is_current_seat_holder")
	}
	if target.role != service.RoleUser && target.role != service.RoleAdmin {
		return reject(service.ErrUserLifecycleInvalid, "target_role_not_lifecycle_managed")
	}
	if target.role == service.RoleAdmin && (actor.role != service.RoleSuperAdmin || in.ActorUserID != seat.userID) {
		return reject(service.ErrUserLifecycleForbidden, "super_admin_required_for_admin_target")
	}
	before := governanceSummary(target.role, target.status, target.sessionVersion)
	switch in.Action {
	case "deactivate_user":
		if target.status != service.StatusActive {
			return reject(service.ErrUserLifecycleInvalid, "target_not_active")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET status='disabled',session_version=session_version+1,updated_at=NOW() WHERE id=$1`, in.TargetUserID); err != nil {
			return err
		}
		// Existing Sub2API keys are irreversibly disabled in the same transaction.
		// Reactivation intentionally never reverses this transition.
		if _, err := tx.ExecContext(ctx, `UPDATE api_keys SET status='disabled',updated_at=NOW() WHERE user_id=$1 AND status<>'disabled' AND deleted_at IS NULL`, in.TargetUserID); err != nil {
			return err
		}
		target.status = service.StatusDisabled
		target.sessionVersion++
	case "reactivate_user":
		if target.status != service.StatusDisabled {
			return reject(service.ErrUserLifecycleInvalid, "target_not_disabled")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET status='active',updated_at=NOW() WHERE id=$1`, in.TargetUserID); err != nil {
			return err
		}
		target.status = service.StatusActive
	default:
		return fmt.Errorf("unknown lifecycle action %q", in.Action)
	}
	if err := insertGovernanceEvent(ctx, tx, in.OperationID, "named_admin", &in.ActorUserID, nil, in.TargetUserID, in.Action, "succeeded", in.Reason, before, governanceSummary(target.role, target.status, target.sessionVersion), nil, nil); err != nil {
		return err
	}
	return governanceCommitError(tx.Commit(), service.ErrUserLifecycleConflict)
}

func (r *governanceRepository) ChangeUserRole(ctx context.Context, in service.AdminRoleChangeInput) error {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	seat, err := lockSeat(ctx, tx)
	if err != nil {
		return err
	}
	users, err := lockGovernanceUsers(ctx, tx, in.ActorUserID, in.TargetUserID)
	if err != nil {
		return err
	}
	actor, actorExists := users[in.ActorUserID]
	target, targetExists := users[in.TargetUserID]
	reject := func(expected error, safe string) error {
		var actorID *int64
		actorKind := "system"
		if actorExists && actor.role == service.RoleSuperAdmin {
			actorID = &in.ActorUserID
			actorKind = "named_admin"
		}
		if eventErr := insertGovernanceEvent(ctx, tx, in.OperationID, actorKind, actorID, nil, in.TargetUserID, "change_admin_identity", "rejected", in.Reason, nil, nil, nil, &safe); eventErr != nil {
			return eventErr
		}
		if commitErr := tx.Commit(); commitErr != nil {
			return governanceCommitError(commitErr, service.ErrUserLifecycleConflict)
		}
		return expected
	}
	if !actorExists || actor.deleted || actor.status != service.StatusActive || actor.role != service.RoleSuperAdmin || in.ActorUserID != seat.userID {
		return reject(service.ErrAdminIdentityForbidden, "current_super_admin_required")
	}
	if !targetExists || target.deleted || in.TargetUserID == seat.userID || target.role == service.RoleSuperAdmin {
		return reject(service.ErrAdminIdentityInvalid, "invalid_or_seat_target")
	}
	if in.TargetRole != service.RoleAdmin && in.TargetRole != service.RoleUser {
		return reject(service.ErrAdminIdentityInvalid, "invalid_target_role")
	}
	if target.role == in.TargetRole {
		return reject(service.ErrAdminIdentityInvalid, "role_unchanged")
	}
	before := governanceSummary(target.role, target.status, target.sessionVersion)
	result, err := tx.ExecContext(ctx, `UPDATE users SET role=$1,session_version=session_version+1,updated_at=NOW() WHERE id=$2 AND deleted_at IS NULL`, in.TargetRole, in.TargetUserID)
	if err != nil {
		return err
	}
	if !exactlyOneRow(result) {
		return service.ErrAdminIdentityInvalid
	}
	if err := insertGovernanceEvent(ctx, tx, in.OperationID, "named_admin", &in.ActorUserID, nil, in.TargetUserID, "change_admin_identity", "succeeded", in.Reason, before, governanceSummary(in.TargetRole, target.status, target.sessionVersion+1), nil, nil); err != nil {
		return err
	}
	return governanceCommitError(tx.Commit(), service.ErrUserLifecycleConflict)
}

func governanceCommitError(err error, conflict error) error {
	if err == nil {
		return nil
	}
	pqErr := (*pq.Error)(nil)
	if errors.As(err, &pqErr) && (pqErr.Code == "40001" || pqErr.Code == "23505" || pqErr.Code == "23514" || pqErr.Code == "P0001") {
		return conflict
	}
	return err
}
