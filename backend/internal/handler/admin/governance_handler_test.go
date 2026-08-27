package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type governanceHandlerRepoStub struct {
	lifecycleCalls     int
	lifecycleRejects   int
	lastLifecycleInput service.UserLifecycleInput
}

func (*governanceHandlerRepoStub) GetSuperAdminSeat(context.Context) (*service.SuperAdminTransferResult, error) {
	return &service.SuperAdminTransferResult{CurrentUserID: 7, SeatVersion: 1}, nil
}
func (*governanceHandlerRepoStub) IsCurrentSuperAdmin(context.Context, int64) (bool, error) {
	return false, nil
}
func (*governanceHandlerRepoStub) RecordGovernanceRejection(context.Context, service.SuperAdminTransferInput, string) error {
	return nil
}
func (*governanceHandlerRepoStub) RecordRecoveryRejection(context.Context, service.EmergencyRecoveryInput, string, bool) error {
	return nil
}
func (s *governanceHandlerRepoStub) RecordUserLifecycleRejection(_ context.Context, in service.UserLifecycleInput, _ string) error {
	s.lifecycleRejects++
	s.lastLifecycleInput = in
	return nil
}
func (*governanceHandlerRepoStub) TransferSuperAdmin(context.Context, service.SuperAdminTransferInput) (*service.SuperAdminTransferResult, error) {
	return nil, nil
}
func (*governanceHandlerRepoStub) EmergencyRecoverSuperAdmin(context.Context, service.EmergencyRecoveryInput) (*service.SuperAdminTransferResult, error) {
	return nil, nil
}
func (s *governanceHandlerRepoStub) ChangeUserLifecycle(_ context.Context, in service.UserLifecycleInput) error {
	s.lifecycleCalls++
	s.lastLifecycleInput = in
	return nil
}
func (*governanceHandlerRepoStub) ChangeUserRole(context.Context, service.AdminRoleChangeInput) error {
	return nil
}

func TestLifecycleHandlerAuditsSharedKeyWithoutNamedAttribution(t *testing.T) {
	repo := &governanceHandlerRepoStub{}
	handler := NewGovernanceHandler(service.NewSuperAdminTransferService(repo, nil, nil))
	router := gin.New()
	router.POST("/users/:id/deactivate", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 99})
		c.Set(string(middleware.ContextKeyUserRole), service.RoleSuperAdmin)
		c.Set("auth_method", "admin_api_key")
	}, handler.DeactivateUser)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/users/42/deactivate", strings.NewReader(`{"reason":"synthetic shared key"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden || repo.lifecycleCalls != 0 || repo.lifecycleRejects != 1 {
		t.Fatalf("status=%d body=%s lifecycle=%d rejects=%d", recorder.Code, recorder.Body.String(), repo.lifecycleCalls, repo.lifecycleRejects)
	}
	if repo.lastLifecycleInput.NamedActor || repo.lastLifecycleInput.TargetUserID != 42 {
		t.Fatalf("unexpected attribution: %+v", repo.lastLifecycleInput)
	}
}

func TestLifecycleHandlerAllowsNamedAdminToReachRepository(t *testing.T) {
	repo := &governanceHandlerRepoStub{}
	handler := NewGovernanceHandler(service.NewSuperAdminTransferService(repo, nil, nil))
	router := gin.New()
	router.POST("/users/:id/deactivate", func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 7})
		c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
		c.Set("auth_method", "jwt")
	}, handler.DeactivateUser)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/users/8/deactivate", strings.NewReader(`{"reason":"synthetic named session"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK || repo.lifecycleCalls != 1 || repo.lifecycleRejects != 0 || !repo.lastLifecycleInput.NamedActor {
		t.Fatalf("status=%d body=%s lifecycle=%d rejects=%d input=%+v", recorder.Code, recorder.Body.String(), repo.lifecycleCalls, repo.lifecycleRejects, repo.lastLifecycleInput)
	}
}

func TestLifecycleHandlerRequiresNonEmptyReason(t *testing.T) {
	repo := &governanceHandlerRepoStub{}
	handler := NewGovernanceHandler(service.NewSuperAdminTransferService(repo, nil, nil))
	router := gin.New()
	router.POST("/users/:id/deactivate", handler.DeactivateUser)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/users/8/deactivate", strings.NewReader(`{"reason":"  "}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest || repo.lifecycleCalls != 0 {
		t.Fatalf("status=%d body=%s calls=%d", recorder.Code, recorder.Body.String(), repo.lifecycleCalls)
	}
}
