package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

type governanceRepoStub struct {
	transferCalls   int
	rejectionCalls  int
	lastRejection   SuperAdminTransferInput
	recoverySafe    string
	recoveryActor   bool
	recoveryCalls   int
	lifecycleCalls  int
	lifecycleReject UserLifecycleInput
	result          *SuperAdminTransferResult
	err             error
	isCurrent       bool
	roleCalls       []AdminRoleChangeInput
	roleChange      func(AdminRoleChangeInput) error
}

func (s *governanceRepoStub) GetSuperAdminSeat(context.Context) (*SuperAdminTransferResult, error) {
	return s.result, s.err
}
func (s *governanceRepoStub) IsCurrentSuperAdmin(context.Context, int64) (bool, error) {
	return s.isCurrent, s.err
}
func (s *governanceRepoStub) RecordGovernanceRejection(_ context.Context, in SuperAdminTransferInput, _ string) error {
	s.rejectionCalls++
	s.lastRejection = in
	return s.err
}
func (s *governanceRepoStub) RecordRecoveryRejection(_ context.Context, _ EmergencyRecoveryInput, safe string, attributed bool) error {
	s.rejectionCalls++
	s.recoverySafe = safe
	s.recoveryActor = attributed
	return s.err
}
func (s *governanceRepoStub) RecordUserLifecycleRejection(_ context.Context, in UserLifecycleInput, _ string) error {
	s.rejectionCalls++
	s.lifecycleReject = in
	return s.err
}
func (s *governanceRepoStub) TransferSuperAdmin(context.Context, SuperAdminTransferInput) (*SuperAdminTransferResult, error) {
	s.transferCalls++
	return s.result, s.err
}
func (s *governanceRepoStub) EmergencyRecoverSuperAdmin(context.Context, EmergencyRecoveryInput) (*SuperAdminTransferResult, error) {
	s.recoveryCalls++
	return s.result, s.err
}
func (s *governanceRepoStub) ChangeUserLifecycle(context.Context, UserLifecycleInput) error {
	s.lifecycleCalls++
	return s.err
}
func (s *governanceRepoStub) ChangeUserRole(_ context.Context, in AdminRoleChangeInput) error {
	s.roleCalls = append(s.roleCalls, in)
	if s.roleChange != nil {
		return s.roleChange(in)
	}
	return s.err
}

type sessionRevokerStub struct{ ids []int64 }

func (s *sessionRevokerStub) RevokeAllUserSessions(_ context.Context, id int64) error {
	s.ids = append(s.ids, id)
	return errors.New("synthetic cache outage")
}

type governanceAuthCacheStub struct{ ids []int64 }

func (*governanceAuthCacheStub) InvalidateAuthCacheByKey(context.Context, string) {}
func (s *governanceAuthCacheStub) InvalidateAuthCacheByUserID(_ context.Context, id int64) {
	s.ids = append(s.ids, id)
}
func (*governanceAuthCacheStub) InvalidateAuthCacheByGroupID(context.Context, int64) {}

func TestAuthorizerCapabilityMatrix(t *testing.T) {
	capabilities := []Capability{
		CapabilityAdminAccess,
		CapabilityManageEmployeeAccounts,
		CapabilityManageAdminIdentities,
		CapabilityTransferSuperAdmin,
		CapabilityRawContentDisclosure,
		CapabilityBreakGlassSeatRecovery,
		CapabilityManageModelCatalog,
		CapabilityManageAutoConfig,
	}
	wants := map[string][]bool{
		RoleSuperAdmin: {true, true, true, true, true, false, true, true},
		RoleAdmin:      {true, true, false, false, false, false, true, true},
		RoleUser:       {false, false, false, false, false, false, false, false},
		"":             {false, false, false, false, false, false, false, false},
	}
	for role, decisions := range wants {
		for i, capability := range capabilities {
			if got := (Authorizer{Role: role}).Has(capability); got != decisions[i] {
				t.Fatalf("role=%q capability=%q got=%v want=%v", role, capability, got, decisions[i])
			}
		}
	}
}

func TestSuperAdminTransferRequiresNamedCurrentSuperAdmin(t *testing.T) {
	for _, tc := range []struct{ role, method string }{{RoleAdmin, "jwt"}, {RoleSuperAdmin, "admin_api_key"}} {
		repo := &governanceRepoStub{}
		svc := &SuperAdminTransferService{repo: repo}
		_, err := svc.Transfer(context.Background(), tc.role, tc.method, SuperAdminTransferInput{ActorUserID: 1, TargetUserID: 2, Reason: "synthetic"})
		if !errors.Is(err, ErrSuperAdminForbidden) {
			t.Fatalf("role=%q method=%q err=%v", tc.role, tc.method, err)
		}
		if repo.transferCalls != 0 || repo.rejectionCalls != 1 {
			t.Fatalf("transfer=%d rejection=%d", repo.transferCalls, repo.rejectionCalls)
		}
		if repo.lastRejection.NamedActor != (tc.method == "jwt") {
			t.Fatalf("named=%v method=%q", repo.lastRejection.NamedActor, tc.method)
		}
	}
}

func TestSuperAdminTransferInvalidatesBothHoldersAfterCommit(t *testing.T) {
	repo := &governanceRepoStub{result: &SuperAdminTransferResult{PreviousUserID: 10, CurrentUserID: 20, SeatVersion: 2}}
	revoker := &sessionRevokerStub{}
	cache := &governanceAuthCacheStub{}
	svc := &SuperAdminTransferService{repo: repo, sessions: revoker, authCache: cache}
	result, err := svc.Transfer(context.Background(), RoleSuperAdmin, "jwt", SuperAdminTransferInput{ActorUserID: 10, TargetUserID: 20, ExpectedSeatVersion: 1, Reason: "synthetic rotation"})
	if err != nil || result.CurrentUserID != 20 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if !reflect.DeepEqual(revoker.ids, []int64{10, 20}) || !reflect.DeepEqual(cache.ids, []int64{10, 20}) {
		t.Fatalf("sessions=%v auth_cache=%v", revoker.ids, cache.ids)
	}
}

func TestLifecycleIsExplicitAndReactivationDoesNotRevokeAgain(t *testing.T) {
	repo := &governanceRepoStub{}
	revoker := &sessionRevokerStub{}
	cache := &governanceAuthCacheStub{}
	svc := &SuperAdminTransferService{repo: repo, sessions: revoker, authCache: cache}
	input := UserLifecycleInput{OperationID: uuid.New(), ActorUserID: 1, TargetUserID: 2, Reason: "synthetic employment change"}
	if err := svc.DeactivateUser(context.Background(), RoleAdmin, "jwt", input); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReactivateUser(context.Background(), RoleAdmin, "jwt", input); err != nil {
		t.Fatal(err)
	}
	if repo.lifecycleCalls != 2 || !reflect.DeepEqual(revoker.ids, []int64{2}) || !reflect.DeepEqual(cache.ids, []int64{2, 2}) {
		t.Fatalf("lifecycle=%d sessions=%v cache=%v", repo.lifecycleCalls, revoker.ids, cache.ids)
	}
}

func TestLifecycleRejectsSharedAdminKeyWithoutNamedAttribution(t *testing.T) {
	repo := &governanceRepoStub{}
	svc := &SuperAdminTransferService{repo: repo}
	err := svc.DeactivateUser(context.Background(), RoleAdmin, "admin_api_key", UserLifecycleInput{ActorUserID: 1, TargetUserID: 2, Reason: "synthetic shared-key attempt"})
	if !errors.Is(err, ErrSuperAdminForbidden) || repo.lifecycleCalls != 0 || repo.rejectionCalls != 1 {
		t.Fatalf("err=%v lifecycle=%d rejection=%d", err, repo.lifecycleCalls, repo.rejectionCalls)
	}
	if repo.lifecycleReject.NamedActor {
		t.Fatal("shared key rejection must not be attributed to a named administrator")
	}
}

func TestEmergencyRecoveryAuthorizationExpirySignatureAndExecution(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	authorization := EmergencyRecoveryAuthorization{TargetUserID: 20, DeploymentOperatorID: "synthetic-operator", Reason: "synthetic lost holder", Nonce: "nonce-0123456789abcdef", ExpiresAt: now.Add(time.Minute)}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(EmergencyRecoverySigningPayload(authorization)))
	authorization.SignatureHex = hex.EncodeToString(mac.Sum(nil))
	repo := &governanceRepoStub{result: &SuperAdminTransferResult{PreviousUserID: 10, CurrentUserID: 20, SeatVersion: 2}}
	svc := &SuperAdminTransferService{repo: repo, now: func() time.Time { return now }}
	result, err := NewEmergencyRecoveryCommand(svc).Execute(context.Background(), authorization, secret)
	if err != nil || result.CurrentUserID != 20 || repo.recoveryCalls != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, repo.recoveryCalls, err)
	}
	authorization.ExpiresAt = now.Add(-time.Second)
	mac = hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(EmergencyRecoverySigningPayload(authorization)))
	authorization.SignatureHex = hex.EncodeToString(mac.Sum(nil))
	if _, err := NewEmergencyRecoveryCommand(svc).Execute(context.Background(), authorization, secret); !errors.Is(err, ErrSuperAdminRecoveryInvalid) {
		t.Fatalf("expired err=%v", err)
	}
	if repo.recoverySafe != "authorization_expired" || !repo.recoveryActor {
		t.Fatalf("safe=%q attributed=%v", repo.recoverySafe, repo.recoveryActor)
	}
}

func TestSessionVersionChangesResolvedJWTVersion(t *testing.T) {
	user := &User{Email: "synthetic-admin@example.invalid", PasswordHash: "synthetic", SessionVersion: 4}
	before := resolvedTokenVersion(user)
	user.SessionVersion++
	if after := resolvedTokenVersion(user); after == before {
		t.Fatalf("resolved JWT version did not change: before=%d after=%d", before, after)
	}
}
