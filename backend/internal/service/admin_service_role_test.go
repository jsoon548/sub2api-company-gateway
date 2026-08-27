//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func governanceStubForUserRepo(repo *userRepoStub, isCurrent bool) *governanceRepoStub {
	return &governanceRepoStub{
		isCurrent: isCurrent,
		roleChange: func(in AdminRoleChangeInput) error {
			if repo.user != nil {
				repo.user.Role = in.TargetRole
			}
			return nil
		},
	}
}

func TestAdminService_CreateUser_WithAdminRoleRequiresCurrentSuperAdmin(t *testing.T) {
	repo := &userRepoStub{nextID: 30}
	governance := governanceStubForUserRepo(repo, false)
	svc := &adminServiceImpl{userRepo: repo, governanceRepo: governance}

	_, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email: "admin@test.com", Password: "strong-pass", Role: RoleAdmin, ActorAdminID: 7,
	})
	require.ErrorIs(t, err, ErrAdminIdentityForbidden)
	require.Empty(t, repo.created, "普通管理员不得创建管理员身份")
	require.Empty(t, governance.roleCalls)
}

func TestAdminService_CreateUser_WithAdminRoleByCurrentSuperAdmin(t *testing.T) {
	repo := &userRepoStub{nextID: 30}
	governance := governanceStubForUserRepo(repo, true)
	svc := &adminServiceImpl{userRepo: repo, governanceRepo: governance}

	user, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email: "admin@test.com", Password: "strong-pass", Role: RoleAdmin, ActorAdminID: 1,
	})
	require.NoError(t, err)
	require.Equal(t, RoleAdmin, user.Role)
	require.Len(t, governance.roleCalls, 1)
	require.Equal(t, int64(1), governance.roleCalls[0].ActorUserID)
	require.Equal(t, RoleAdmin, governance.roleCalls[0].TargetRole)
}

func TestAdminService_CreateUser_DefaultsToUserRole(t *testing.T) {
	repo := &userRepoStub{nextID: 31}
	svc := &adminServiceImpl{userRepo: repo}

	user, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email: "plain@test.com", Password: "strong-pass",
	})
	require.NoError(t, err)
	require.Equal(t, RoleUser, user.Role)
}

func TestAdminService_CreateUser_InvalidRoleRejected(t *testing.T) {
	repo := &userRepoStub{nextID: 32}
	svc := &adminServiceImpl{userRepo: repo}

	_, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email: "bad@test.com", Password: "strong-pass", Role: "superuser",
	})
	require.Error(t, err)
	require.Empty(t, repo.created, "非法角色不应写入用户")
}

func TestAdminService_UpdateUser_PromoteToAdminByCurrentSuperAdmin(t *testing.T) {
	base := &userRepoStub{user: &User{ID: 42, Email: "u@example.com", Role: RoleUser}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	governance := governanceStubForUserRepo(base, true)
	invalidator := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{
		userRepo: repo, redeemCodeRepo: &redeemRepoStub{}, governanceRepo: governance,
		authCacheInvalidator: invalidator,
	}

	updated, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{Role: RoleAdmin, ActorAdminID: 1})
	require.NoError(t, err)
	require.Equal(t, RoleAdmin, updated.Role)
	require.Len(t, governance.roleCalls, 1)
	require.Equal(t, []int64{42}, invalidator.userIDs, "角色变更应失效认证缓存")
}

func TestAdminService_UpdateUser_PromoteToAdminByOrdinaryAdminRejected(t *testing.T) {
	base := &userRepoStub{user: &User{ID: 42, Email: "u@example.com", Role: RoleUser}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	governance := governanceStubForUserRepo(base, false)
	svc := &adminServiceImpl{userRepo: repo, redeemCodeRepo: &redeemRepoStub{}, governanceRepo: governance}

	_, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{Role: RoleAdmin, ActorAdminID: 2})
	require.ErrorIs(t, err, ErrAdminIdentityForbidden)
	require.Empty(t, governance.roleCalls)
	require.Nil(t, repo.lastUpdated)
}

func TestAdminService_UpdateUser_RoleOmittedKeepsExisting(t *testing.T) {
	base := &userRepoStub{user: &User{ID: 42, Email: "u@example.com", Role: RoleAdmin}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	governance := governanceStubForUserRepo(base, true)
	svc := &adminServiceImpl{userRepo: repo, redeemCodeRepo: &redeemRepoStub{}, governanceRepo: governance}

	newName := "renamed"
	updated, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{Username: &newName, ActorAdminID: 1})
	require.NoError(t, err)
	require.Equal(t, RoleAdmin, updated.Role, "未提供 role 时不应改变现有角色")
	require.Empty(t, governance.roleCalls)
}

func TestAdminService_UpdateUser_InvalidRoleRejected(t *testing.T) {
	base := &userRepoStub{user: &User{ID: 42, Email: "u@example.com", Role: RoleUser}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	svc := &adminServiceImpl{userRepo: repo, redeemCodeRepo: &redeemRepoStub{}}

	_, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{Role: "root"})
	require.Error(t, err)
	require.Nil(t, repo.lastUpdated, "非法角色不应触发持久化")
}

func TestAdminService_UpdateUser_DemoteAdminByCurrentSuperAdmin(t *testing.T) {
	base := &userRepoStub{user: &User{ID: 42, Email: "a@example.com", Role: RoleAdmin}}
	repo := &rpmUserRepoStub{userRepoStub: base}
	governance := governanceStubForUserRepo(base, true)
	svc := &adminServiceImpl{
		userRepo: repo, redeemCodeRepo: &redeemRepoStub{}, governanceRepo: governance,
		authCacheInvalidator: &authCacheInvalidatorStub{},
	}

	updated, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{Role: RoleUser, ActorAdminID: 1})
	require.NoError(t, err)
	require.Equal(t, RoleUser, updated.Role)
	require.Len(t, governance.roleCalls, 1)
	require.Equal(t, RoleUser, governance.roleCalls[0].TargetRole)
}
