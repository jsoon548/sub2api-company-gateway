package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

var (
	ErrSuperAdminForbidden          = infraerrors.Forbidden("SUPER_ADMIN_FORBIDDEN", "super administrator named session required")
	ErrSuperAdminTargetInvalid      = infraerrors.BadRequest("SUPER_ADMIN_TARGET_INVALID", "target must be a different active administrator")
	ErrSuperAdminConflict           = infraerrors.Conflict("SUPER_ADMIN_TRANSFER_CONFLICT", "super administrator seat changed concurrently")
	ErrSuperAdminRecoveryInvalid    = infraerrors.Forbidden("SUPER_ADMIN_RECOVERY_INVALID", "emergency recovery authorization is invalid")
	ErrUserLifecycleForbidden       = infraerrors.Forbidden("USER_LIFECYCLE_FORBIDDEN", "not authorized to change target account lifecycle")
	ErrUserLifecycleInvalid         = infraerrors.BadRequest("USER_LIFECYCLE_INVALID", "target account cannot perform the requested lifecycle transition")
	ErrUserLifecycleConflict        = infraerrors.Conflict("USER_LIFECYCLE_CONFLICT", "account lifecycle changed concurrently")
	ErrAdminIdentityForbidden       = infraerrors.Forbidden("ADMIN_IDENTITY_FORBIDDEN", "current super administrator session required to manage administrator identities")
	ErrAdminIdentityInvalid         = infraerrors.BadRequest("ADMIN_IDENTITY_INVALID", "administrator role transition is invalid")
	ErrPhysicalUserDeletionDisabled = infraerrors.Forbidden("USER_DELETE_DISABLED", "physical user deletion is disabled")
)

type SuperAdminTransferInput struct {
	OperationID         uuid.UUID
	ActorUserID         int64
	TargetUserID        int64
	ExpectedSeatVersion int64
	Reason              string
	GatewayRequestID    *string
	NamedActor          bool
}

type EmergencyRecoveryInput struct {
	OperationID          uuid.UUID
	TargetUserID         int64
	DeploymentOperatorID string
	Reason               string
	NonceFingerprint     string
}

type UserLifecycleInput struct {
	OperationID  uuid.UUID
	ActorUserID  int64
	TargetUserID int64
	Reason       string
	Action       string
	NamedActor   bool
}

type AdminRoleChangeInput struct {
	OperationID  uuid.UUID
	ActorUserID  int64
	TargetUserID int64
	TargetRole   string
	Reason       string
}

type SuperAdminTransferResult struct {
	PreviousUserID int64 `json:"previous_user_id"`
	CurrentUserID  int64 `json:"current_user_id"`
	SeatVersion    int64 `json:"seat_version"`
}

// GovernanceRepository owns the transaction boundary for role, seat and
// lifecycle mutations together with their append-only governance event.
type GovernanceRepository interface {
	GetSuperAdminSeat(context.Context) (*SuperAdminTransferResult, error)
	IsCurrentSuperAdmin(context.Context, int64) (bool, error)
	RecordGovernanceRejection(context.Context, SuperAdminTransferInput, string) error
	RecordRecoveryRejection(context.Context, EmergencyRecoveryInput, string, bool) error
	RecordUserLifecycleRejection(context.Context, UserLifecycleInput, string) error
	TransferSuperAdmin(context.Context, SuperAdminTransferInput) (*SuperAdminTransferResult, error)
	EmergencyRecoverSuperAdmin(context.Context, EmergencyRecoveryInput) (*SuperAdminTransferResult, error)
	ChangeUserLifecycle(context.Context, UserLifecycleInput) error
	ChangeUserRole(context.Context, AdminRoleChangeInput) error
}

type UserSessionRevoker interface {
	RevokeAllUserSessions(context.Context, int64) error
}

type SuperAdminTransferService struct {
	repo      GovernanceRepository
	sessions  UserSessionRevoker
	authCache APIKeyAuthCacheInvalidator
	now       func() time.Time
}

func NewSuperAdminTransferService(repo GovernanceRepository, auth *AuthService, authCache APIKeyAuthCacheInvalidator) *SuperAdminTransferService {
	service := &SuperAdminTransferService{repo: repo, authCache: authCache, now: time.Now}
	if auth != nil {
		service.sessions = auth
	}
	return service
}

func (s *SuperAdminTransferService) CurrentSeat(ctx context.Context) (*SuperAdminTransferResult, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("governance repository unavailable")
	}
	return s.repo.GetSuperAdminSeat(ctx)
}

func (s *SuperAdminTransferService) Transfer(ctx context.Context, actorRole, authMethod string, input SuperAdminTransferInput) (*SuperAdminTransferResult, error) {
	if s == nil || s.repo == nil {
		return nil, errors.New("governance repository unavailable")
	}
	if input.OperationID == uuid.Nil {
		input.OperationID = uuid.New()
	}
	input.NamedActor = authMethod == "jwt"
	if !(Authorizer{Role: actorRole}).Has(CapabilityTransferSuperAdmin) || !input.NamedActor {
		if err := s.repo.RecordGovernanceRejection(ctx, input, "named_super_admin_session_required"); err != nil {
			return nil, err
		}
		return nil, ErrSuperAdminForbidden
	}
	input.Reason = strings.TrimSpace(input.Reason)
	result, err := s.repo.TransferSuperAdmin(ctx, input)
	if err != nil {
		return nil, err
	}
	s.revokePostCommit(ctx, result.PreviousUserID)
	s.revokePostCommit(ctx, result.CurrentUserID)
	return result, nil
}

func (s *SuperAdminTransferService) DeactivateUser(ctx context.Context, actorRole, authMethod string, input UserLifecycleInput) error {
	input.Action = "deactivate_user"
	if err := s.prepareUserLifecycle(ctx, actorRole, authMethod, &input); err != nil {
		return err
	}
	if err := s.repo.ChangeUserLifecycle(ctx, input); err != nil {
		return err
	}
	s.revokePostCommit(ctx, input.TargetUserID)
	return nil
}

func (s *SuperAdminTransferService) ReactivateUser(ctx context.Context, actorRole, authMethod string, input UserLifecycleInput) error {
	input.Action = "reactivate_user"
	if err := s.prepareUserLifecycle(ctx, actorRole, authMethod, &input); err != nil {
		return err
	}
	if err := s.repo.ChangeUserLifecycle(ctx, input); err != nil {
		return err
	}
	if s.authCache != nil {
		s.authCache.InvalidateAuthCacheByUserID(ctx, input.TargetUserID)
	}
	return nil
}

func (s *SuperAdminTransferService) revokePostCommit(ctx context.Context, userID int64) {
	// session_version and API-key status in PostgreSQL are authoritative. Redis
	// cleanup is a post-commit acceleration and cannot undo the committed change.
	if s.sessions != nil {
		_ = s.sessions.RevokeAllUserSessions(ctx, userID)
	}
	if s.authCache != nil {
		s.authCache.InvalidateAuthCacheByUserID(ctx, userID)
	}
}

func (s *SuperAdminTransferService) prepareUserLifecycle(ctx context.Context, actorRole, authMethod string, input *UserLifecycleInput) error {
	if s == nil || s.repo == nil {
		return errors.New("governance repository unavailable")
	}
	if input.OperationID == uuid.Nil {
		input.OperationID = uuid.New()
	}
	input.Reason = strings.TrimSpace(input.Reason)
	input.NamedActor = authMethod == "jwt" && (actorRole == RoleAdmin || actorRole == RoleSuperAdmin)
	if !(Authorizer{Role: actorRole}).Has(CapabilityManageEmployeeAccounts) || authMethod != "jwt" {
		if err := s.repo.RecordUserLifecycleRejection(ctx, *input, "named_admin_session_required"); err != nil {
			return err
		}
		return ErrSuperAdminForbidden
	}
	return nil
}

// EmergencyRecoveryCommand is deliberately deployment-only. Its signature is
// verified before any repository call and its nonce fingerprint is persisted
// under a unique constraint to reject replay.
type EmergencyRecoveryCommand struct{ service *SuperAdminTransferService }

func NewEmergencyRecoveryCommand(service *SuperAdminTransferService) *EmergencyRecoveryCommand {
	return &EmergencyRecoveryCommand{service: service}
}

type EmergencyRecoveryAuthorization struct {
	TargetUserID         int64
	DeploymentOperatorID string
	Reason               string
	Nonce                string
	ExpiresAt            time.Time
	SignatureHex         string
}

func EmergencyRecoverySigningPayload(a EmergencyRecoveryAuthorization) string {
	return strconv.FormatInt(a.TargetUserID, 10) + "\x00" + strings.TrimSpace(a.DeploymentOperatorID) + "\x00" + strings.TrimSpace(a.Reason) + "\x00" + a.Nonce + "\x00" + a.ExpiresAt.UTC().Format(time.RFC3339)
}

func (c *EmergencyRecoveryCommand) Execute(ctx context.Context, authorization EmergencyRecoveryAuthorization, secret []byte) (*SuperAdminTransferResult, error) {
	if c == nil || c.service == nil || c.service.repo == nil {
		return nil, ErrSuperAdminRecoveryInvalid
	}
	reject := func(in EmergencyRecoveryInput, safeReason string, attributed bool) (*SuperAdminTransferResult, error) {
		if err := c.service.repo.RecordRecoveryRejection(ctx, in, safeReason, attributed); err != nil {
			return nil, err
		}
		return nil, ErrSuperAdminRecoveryInvalid
	}
	baseInput := EmergencyRecoveryInput{
		OperationID: uuid.New(), TargetUserID: authorization.TargetUserID,
		DeploymentOperatorID: strings.TrimSpace(authorization.DeploymentOperatorID),
	}
	if len(secret) < 32 {
		return reject(baseInput, "recovery_secret_unavailable", false)
	}
	if authorization.TargetUserID <= 0 || baseInput.DeploymentOperatorID == "" || strings.TrimSpace(authorization.Reason) == "" || len(authorization.Nonce) < 16 {
		return reject(baseInput, "invalid_authorization_shape", false)
	}
	want, err := hex.DecodeString(strings.TrimSpace(authorization.SignatureHex))
	if err != nil {
		return reject(baseInput, "invalid_signature_encoding", false)
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(EmergencyRecoverySigningPayload(authorization)))
	if !hmac.Equal(want, mac.Sum(nil)) {
		return reject(baseInput, "invalid_signature", false)
	}
	nonceHash := sha256.Sum256([]byte(authorization.Nonce))
	baseInput.Reason = strings.TrimSpace(authorization.Reason)
	baseInput.NonceFingerprint = hex.EncodeToString(nonceHash[:])
	if !authorization.ExpiresAt.After(c.service.now().UTC()) {
		return reject(baseInput, "authorization_expired", true)
	}
	result, err := c.service.repo.EmergencyRecoverSuperAdmin(ctx, baseInput)
	if err != nil {
		return nil, fmt.Errorf("emergency recovery: %w", err)
	}
	c.service.revokePostCommit(ctx, result.PreviousUserID)
	c.service.revokePostCommit(ctx, result.CurrentUserID)
	return result, nil
}
