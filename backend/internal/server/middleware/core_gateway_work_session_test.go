//go:build unit

package middleware

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type workSessionMiddlewareRepo struct {
	auto         service.AutoConfig
	associations int
}

func (*workSessionMiddlewareRepo) CheckFoundation(context.Context) error            { return nil }
func (*workSessionMiddlewareRepo) CurrentGeneration(context.Context) (int64, error) { return 1, nil }
func (r *workSessionMiddlewareRepo) FindOrCreateReliable(_ context.Context, in service.WorkSessionCreate) (service.WorkSessionRecord, error) {
	r.associations++
	return service.WorkSessionRecord{ID: in.ID, Reliability: service.WorkSessionReliabilityReliable, AnalysisEligible: true}, nil
}
func (r *workSessionMiddlewareRepo) CreateUnreliable(_ context.Context, in service.WorkSessionCreate) (service.WorkSessionRecord, error) {
	r.associations++
	return service.WorkSessionRecord{ID: in.ID, Reliability: service.WorkSessionReliabilityUnreliable, Status: "request_scoped"}, nil
}
func (r *workSessionMiddlewareRepo) GetAutoConfig(context.Context) (service.AutoConfig, error) {
	return r.auto, nil
}
func (*workSessionMiddlewareRepo) ReplaceManagementConfig(context.Context, service.WorkSessionManagementUpdate, time.Time) (int64, error) {
	return 1, nil
}
func (*workSessionMiddlewareRepo) ListManagement(context.Context, int) (service.AutoConfig, []service.ModelCatalogEntry, []service.AutoCandidate, []service.WorkSessionRecord, error) {
	return service.AutoConfig{}, nil, nil, nil, nil
}
func (*workSessionMiddlewareRepo) SetEmergencyDisabled(context.Context, string, bool, time.Time) error {
	return nil
}
func (*workSessionMiddlewareRepo) IsModelAvailableForSession(context.Context, uuid.UUID, string, time.Time) (bool, error) {
	return true, nil
}
func (*workSessionMiddlewareRepo) LinkGatewayRequest(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func workSessionMiddlewareService(t *testing.T, repo *workSessionMiddlewareRepo, withKey bool) *service.WorkSessionService {
	t.Helper()
	keyRef := "env:WORK_SESSION_WORK_SESSION_HMAC_MISSING"
	if withKey {
		keyRef = "env:WORK_SESSION_WORK_SESSION_HMAC_MIDDLEWARE_KEY"
		t.Setenv("WORK_SESSION_WORK_SESSION_HMAC_MIDDLEWARE_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	}
	svc := service.NewWorkSessionService(repo, config.WorkSessionConfig{Mode: service.WorkSessionModeRequired, TenantID: "tenant-test", HMACKeyRef: keyRef, HMACKeyVersion: "workSession-v1"}, config.AuditConfig{}, config.InternalInferenceConfig{}, nil)
	svc.Start()
	return svc
}

func workSessionMiddlewareRequest(t *testing.T, svc *service.WorkSessionService, model, sessionHeader string) (int, string, int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	downstream := 0
	router := gin.New()
	router.POST("/v1/responses", CoreGatewayRequestID(), func(c *gin.Context) {
		groupID := int64(9)
		c.Set(string(ContextKeyAPIKey), &service.APIKey{GroupID: &groupID, User: &service.User{ID: 42, Status: service.StatusActive, Role: service.RoleUser}})
		c.Next()
	}, CoreGatewayWorkSessionAdmission(svc, service.ProtocolProfileOpenAIResponsesV1), func(c *gin.Context) {
		downstream++
		c.Status(http.StatusNoContent)
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"`+model+`"}`))
	if sessionHeader != "" {
		req.Header.Set("Session-Id", sessionHeader)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w.Code, w.Body.String(), downstream
}

func TestWorkSessionMiddlewareExplicitSurvivesMissingHMACButAutoFailsClosed(t *testing.T) {
	repo := &workSessionMiddlewareRepo{}
	svc := workSessionMiddlewareService(t, repo, false)
	status, _, downstream := workSessionMiddlewareRequest(t, svc, "explicit-model", uuid.NewString())
	require.Equal(t, http.StatusNoContent, status)
	require.Equal(t, 1, downstream)

	status, body, downstream := workSessionMiddlewareRequest(t, svc, "auto", uuid.NewString())
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Contains(t, body, "gateway_auto_unavailable")
	require.Zero(t, downstream)
}

func TestWorkSessionMiddlewareAutoBoundaryRejectsDisabledAndUnreliableAndNeverRoutes(t *testing.T) {
	repo := &workSessionMiddlewareRepo{auto: service.AutoConfig{Enabled: false, UserWhitelist: []int64{42}, ConfigVersion: 1}}
	svc := workSessionMiddlewareService(t, repo, true)
	status, body, downstream := workSessionMiddlewareRequest(t, svc, "auto", uuid.NewString())
	require.Equal(t, http.StatusForbidden, status)
	require.Contains(t, body, "gateway_auto_not_allowed")
	require.Zero(t, downstream)

	repo.auto.Enabled = true
	status, body, downstream = workSessionMiddlewareRequest(t, svc, "auto", "")
	require.Equal(t, http.StatusForbidden, status)
	require.Contains(t, body, "gateway_auto_not_allowed")
	require.Zero(t, downstream)

	status, body, downstream = workSessionMiddlewareRequest(t, svc, "auto", uuid.NewString())
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Contains(t, body, "gateway_auto_routing_unavailable")
	require.Zero(t, downstream, "work-session implementation must never route model=auto upstream")
}
