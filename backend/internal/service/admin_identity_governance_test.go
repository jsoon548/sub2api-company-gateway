//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminIdentityBindingRequiresCurrentSuperAdminForAdminTarget(t *testing.T) {
	client := newAdminServiceAuthIdentityBindingTestClient(t)
	ctx := context.Background()
	target, err := client.User.Create().
		SetEmail("synthetic-admin-target@example.invalid").
		SetPasswordHash("synthetic").
		SetRole(RoleAdmin).
		SetStatus(StatusActive).
		Save(ctx)
	require.NoError(t, err)

	input := AdminBindAuthIdentityInput{
		ActorAdminID: 11, ProviderType: "oidc", ProviderKey: "https://synthetic.invalid",
		ProviderSubject: "synthetic-admin-subject",
	}
	denied := &adminServiceImpl{
		userRepo:       &userRepoStub{user: &User{ID: target.ID, Email: target.Email, Role: RoleAdmin, Status: StatusActive}},
		governanceRepo: &governanceRepoStub{isCurrent: false},
		entClient:      client,
	}
	_, err = denied.BindUserAuthIdentity(ctx, target.ID, input)
	require.ErrorIs(t, err, ErrAdminIdentityForbidden)
	count, countErr := client.AuthIdentity.Query().Count(ctx)
	require.NoError(t, countErr)
	require.Zero(t, count)

	allowed := &adminServiceImpl{
		userRepo:       &userRepoStub{user: &User{ID: target.ID, Email: target.Email, Role: RoleAdmin, Status: StatusActive}},
		governanceRepo: &governanceRepoStub{isCurrent: true},
		entClient:      client,
	}
	result, err := allowed.BindUserAuthIdentity(ctx, target.ID, input)
	require.NoError(t, err)
	require.Equal(t, target.ID, result.UserID)
	count, err = client.AuthIdentity.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestPhysicalUserDeletionIsDisabled(t *testing.T) {
	svc := &adminServiceImpl{}
	err := svc.DeleteUser(context.Background(), 42)
	if !errors.Is(err, ErrPhysicalUserDeletionDisabled) {
		t.Fatalf("err=%v", err)
	}
}

func TestAPIKeyCredentialEpochRejectsStaleCachedSnapshotAfterLifecycleChange(t *testing.T) {
	current := &User{ID: 42, Role: RoleUser, Status: StatusActive, SessionVersion: 3}
	svc := &APIKeyService{userRepo: &userRepoStub{user: current}}
	cached := &APIKey{
		ID: 9, UserID: current.ID, Status: StatusAPIKeyActive,
		User: &User{ID: current.ID, Role: RoleUser, Status: StatusActive, SessionVersion: 2},
	}
	got, err := svc.enforceCurrentCredentialEpoch(context.Background(), cached)
	require.NoError(t, err)
	require.Equal(t, StatusAPIKeyDisabled, got.Status)
	require.Equal(t, int64(3), got.User.SessionVersion)
}
