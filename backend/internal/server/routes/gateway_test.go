package routes

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type gatewayRoutesCatalogStub struct {
	platform string
	calls    *int
}

func (s gatewayRoutesCatalogStub) FindApprovedExplicitModel(_ context.Context, input service.ExplicitModelResolveInput) (*service.ExplicitModelApprovalSnapshot, error) {
	if s.calls != nil {
		*s.calls++
	}
	return &service.ExplicitModelApprovalSnapshot{
		EntryID:                 "test-entry",
		GroupID:                 input.Access.GroupID,
		ChannelID:               1,
		LogicalModel:            input.RequestedLogicalModel,
		Platform:                s.platform,
		ResolvedProviderModel:   input.RequestedLogicalModel,
		SchedulableAccountScope: []int64{1},
		ConfigurationVersion:    1,
	}, nil
}

func newGatewayRoutesTestRouter(platform ...string) *gin.Engine {
	return newGatewayRoutesTestRouterWithCatalogCounter(nil, platform...)
}

func newGatewayRoutesTestRouterWithCatalogCounter(calls *int, platform ...string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	groupPlatform := service.PlatformOpenAI
	if len(platform) > 0 && platform[0] != "" {
		groupPlatform = platform[0]
	}
	auditFoundation := service.NewAuditFoundationService(nil, config.AuditConfig{Mode: service.AuditModeDisabled})
	auditFoundation.Start()

	RegisterGatewayRoutes(
		router,
		&handler.Handlers{
			Gateway:       &handler.GatewayHandler{},
			OpenAIGateway: &handler.OpenAIGatewayHandler{},
		},
		servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
			groupID := int64(1)
			c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
				GroupID: &groupID,
				Group:   &service.Group{ID: groupID, Platform: groupPlatform, Status: service.StatusActive, AllowMessagesDispatch: true},
				User:    &service.User{ID: 1, Status: service.StatusActive, Role: service.RoleUser},
			})
			c.Next()
		}),
		nil,
		nil,
		nil,
		nil,
		service.NewExplicitModelResolver(gatewayRoutesCatalogStub{platform: groupPlatform, calls: calls}),
		auditFoundation,
		nil,
		&config.Config{Gateway: config.GatewayConfig{MaxBodySize: 1 << 20}},
	)

	return router
}

func TestAuditFoundationAuditGateStopsBeforeExplicitModelResolver(t *testing.T) {
	modelResolutionCalls := 0
	router := newGatewayRoutesTestRouterWithCatalogCounter(&modelResolutionCalls)

	for _, path := range []string{"/v1/messages", "/v1/messages/count_tokens", "/v1/responses", "/responses"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"synthetic-model","input":"synthetic"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusServiceUnavailable, w.Code, "path=%s", path)
		require.Contains(t, w.Body.String(), "gateway_audit_unavailable", "path=%s", path)
	}
	require.Zero(t, modelResolutionCalls)
}

func TestGatewayRoutesUnapprovedResponsesEntrypointsStayClosed(t *testing.T) {
	router := newGatewayRoutesTestRouter()

	for _, path := range []string{
		"/backend-api/codex/responses",
		"/backend-api/codex/responses/compact",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-5"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusForbidden, w.Code, "path=%s must stay closed", path)
		require.Contains(t, w.Body.String(), "gateway_entry_not_allowed")
	}
}

func TestClientProfileCompactEntrypointsReachAuditBeforeModelResolution(t *testing.T) {
	modelResolutionCalls := 0
	router := newGatewayRoutesTestRouterWithCatalogCounter(&modelResolutionCalls)
	for _, path := range []string{"/v1/responses/compact", "/responses/compact"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"clientProfile-synthetic-model","input":"synthetic"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusServiceUnavailable, w.Code, "path=%s", path)
		require.Contains(t, w.Body.String(), "gateway_audit_unavailable")
	}
	require.Zero(t, modelResolutionCalls)
}

func TestGatewayRoutesChatCompletionsAndMessagesAliasStayClosed(t *testing.T) {
	router := newGatewayRoutesTestRouter()

	for _, path := range []string{"/v1/chat/completions", "/chat/completions", "/messages", "/v1/messages/unknown"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-5"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusForbidden, w.Code, "path=%s must stay closed", path)
		require.Contains(t, w.Body.String(), "gateway_entry_not_allowed")
	}
}

func TestGatewayRoutesOpenAIImagesPathsAreRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter()

	for _, path := range []string{
		"/v1/images/generations",
		"/v1/images/edits",
		"/images/generations",
		"/images/edits",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-image-2","prompt":"draw a cat"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit OpenAI images handler", path)
	}
}

func TestGatewayRoutesGrokImagesAndVideosPathsAreRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter(service.PlatformGrok)

	for _, path := range []string{
		"/v1/images/generations",
		"/v1/images/edits",
		"/images/generations",
		"/images/edits",
		"/v1/videos/generations",
		"/videos/generations",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"grok-imagine","prompt":"draw a cat"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit Grok media handler", path)
		require.NotContains(t, w.Body.String(), "not supported for this platform")
	}

	for _, path := range []string{
		"/v1/videos/request-123",
		"/videos/request-123",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit Grok video handler", path)
		require.NotContains(t, w.Body.String(), "not supported for this platform")
	}
}

func TestGatewayRoutesNonGrokVideosAreRejectedAtPlatformGate(t *testing.T) {
	router := newGatewayRoutesTestRouter(service.PlatformOpenAI)

	for _, tc := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/v1/videos/generations", `{"model":"grok-imagine-video-1.5","prompt":"waves"}`},
		{http.MethodPost, "/videos/generations", `{"model":"grok-imagine-video-1.5","prompt":"waves"}`},
		{http.MethodGet, "/v1/videos/request-123", ""},
		{http.MethodGet, "/videos/request-123", ""},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusNotFound, w.Code, "method=%s path=%s", tc.method, tc.path)
		require.Contains(t, w.Body.String(), "Videos API is not supported for this platform")
	}
}

func TestGatewayRoutesGrokAllowsCLICompatibilityEntrypoints(t *testing.T) {
	router := newGatewayRoutesTestRouter(service.PlatformGrok)

	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/v1/messages"},
		{http.MethodPost, "/v1/chat/completions"},
		{http.MethodPost, "/chat/completions"},
		{http.MethodGet, "/v1/responses"},
		{http.MethodGet, "/responses"},
		{http.MethodGet, "/backend-api/codex/responses"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"model":"grok"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "method=%s path=%s", tc.method, tc.path)
		require.NotContains(t, w.Body.String(), "not supported for Grok groups")
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{"model":"grok","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), "gateway_audit_unavailable")

	for _, path := range []string{
		"/v1/responses",
		"/responses",
		"/backend-api/codex/responses",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"grok","input":"hi"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should still reach Responses handler", path)
	}
}

func TestGatewayRoutesOpenAICountTokensPathIsRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter(service.PlatformOpenAI)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Contains(t, w.Body.String(), "gateway_audit_unavailable")
}
