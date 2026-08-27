package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestStrictEntryProfileDoesNotUseUserAgentForAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, userAgent := range []string{"claude-cli/2.synthetic", "opencode/1.synthetic", "unidentified-client"} {
		t.Run(userAgent, func(t *testing.T) {
			upstream := 0
			router := gin.New()
			router.POST("/v1/messages", CoreGatewayStrictEntryProfile(service.ProtocolProfileAnthropicMessagesV1, CoreGatewayEntryShape{Path: "/v1/messages", AllowHTTP: true, AllowSSE: true, RequireAPI: CoreGatewayAPIAnthropicVersion}), func(c *gin.Context) {
				upstream++
				c.Status(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"clientProfile-synthetic-model","messages":[]}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("anthropic-version", "2023-06-01")
			req.Header.Set("User-Agent", userAgent)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusNoContent, w.Code)
			require.Equal(t, 1, upstream)
		})
	}
}

func TestStrictEntryProfileRejectsProtocolShapeBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name, contentType, version, body string
		upgrade                          bool
		wantStatus                       int
	}{
		{name: "missing protocol header", contentType: "application/json", body: `{}`, wantStatus: http.StatusBadRequest},
		{name: "wrong content type", contentType: "text/plain", version: "2023-06-01", body: `{}`, wantStatus: http.StatusForbidden},
		{name: "invalid json", contentType: "application/json", version: "2023-06-01", body: `{`, wantStatus: http.StatusBadRequest},
		{name: "invalid stream type", contentType: "application/json", version: "2023-06-01", body: `{"stream":"yes"}`, wantStatus: http.StatusBadRequest},
		{name: "websocket upgrade", contentType: "application/json", version: "2023-06-01", body: `{}`, upgrade: true, wantStatus: http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := 0
			router := gin.New()
			router.POST("/v1/messages", CoreGatewayRequestID(), CoreGatewayStrictEntryProfile(service.ProtocolProfileAnthropicMessagesV1, CoreGatewayEntryShape{Path: "/v1/messages", AllowHTTP: true, AllowSSE: true, RequireAPI: CoreGatewayAPIAnthropicVersion}), func(c *gin.Context) { upstream++ })
			req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)
			req.Header.Set("anthropic-version", tt.version)
			if tt.upgrade {
				req.Header.Set("Connection", "Upgrade")
				req.Header.Set("Upgrade", "websocket")
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, tt.wantStatus, w.Code)
			require.Zero(t, upstream)
			require.NotEmpty(t, w.Header().Get(GatewayRequestIDHeader))
			require.Contains(t, w.Body.String(), w.Header().Get(GatewayRequestIDHeader))
		})
	}
}

func TestStrictCountTokensRejectsSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := 0
	router := gin.New()
	router.POST("/v1/messages/count_tokens", CoreGatewayRequestID(), CoreGatewayStrictEntryProfile(service.ProtocolProfileAnthropicMessagesV1, CoreGatewayEntryShape{Path: "/v1/messages/count_tokens", AllowHTTP: true, RequireAPI: CoreGatewayAPIAnthropicVersion}), func(c *gin.Context) { upstream++ })
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{"model":"clientProfile-synthetic-model","stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Zero(t, upstream)
}

func TestStrictProfileProtocolErrorIsFullyAudited(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &auditContinuityRepoStub{}
	svc := newReadyContinuityService(t, repo)
	upstream := 0
	router := gin.New()
	router.POST("/v1/messages", fixedAuditContinuityRequestID, setAuditAdmissionAPIKey,
		CoreGatewayAuditAdmission(svc, service.ProtocolProfileAnthropicMessagesV1),
		CoreGatewayAuditContinuity(svc, service.ProtocolProfileAnthropicMessagesV1),
		CoreGatewayStrictEntryProfile(service.ProtocolProfileAnthropicMessagesV1, CoreGatewayEntryShape{Path: "/v1/messages", AllowHTTP: true, AllowSSE: true, RequireAPI: CoreGatewayAPIAnthropicVersion}),
		func(c *gin.Context) { upstream++ })
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"clientProfile-synthetic-model","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Zero(t, upstream)
	require.Equal(t, auditContinuityFixedGatewayID.String(), w.Header().Get(GatewayRequestIDHeader))
	require.Contains(t, w.Body.String(), auditContinuityFixedGatewayID.String())
	require.Len(t, repo.responseCommits, 1)
	require.Len(t, repo.finalizations, 1)
	require.Equal(t, service.AuditRequestRejectedPreUpstream, repo.finalizations[0].RequestOutcome)
	require.Equal(t, service.AuditContentComplete, repo.finalizations[0].ContentState)
	_, auditedBody := decryptResponseParts(t, svc, repo)
	require.Equal(t, w.Body.Bytes(), auditedBody)
}

func TestCompactRequiresResolvedOpenAIPlatform(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformAnthropic} {
		t.Run(platform, func(t *testing.T) {
			upstream := 0
			router := gin.New()
			router.POST("/v1/responses/compact", CoreGatewayRequestID(), func(c *gin.Context) {
				resolution := service.ExplicitModelResolution{RequestedLogicalModel: "clientProfile-synthetic-model", Platform: platform, ResolvedProviderModel: "clientProfile-synthetic-model", SchedulableAccountScope: []int64{1}}
				c.Request = c.Request.WithContext(service.WithExplicitModelResolution(context.Background(), resolution))
				c.Next()
			}, CoreGatewayRequireResolvedPlatform(service.PlatformOpenAI), func(c *gin.Context) { upstream++ })
			req := httptest.NewRequest(http.MethodPost, "/v1/responses/compact", strings.NewReader(`{}`))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if platform == service.PlatformOpenAI {
				require.Equal(t, 1, upstream)
			} else {
				require.Zero(t, upstream)
				require.Equal(t, http.StatusForbidden, w.Code)
			}
		})
	}
}

func TestNoRouteDefaultDenyReturnsNativeErrorAndGatewayID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.NoRoute(CoreGatewayNoRouteDefaultDeny())
	for _, tc := range []struct{ method, path, marker string }{
		{http.MethodPut, "/v1/messages", `"type":"error"`},
		{http.MethodPost, "/v1/messages/arbitrary", `"type":"error"`},
		{http.MethodDelete, "/responses/arbitrary", `"error"`},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"model":"clientProfile-synthetic-model"}`))
		req.Header.Set("User-Agent", "opencode/1.synthetic")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusForbidden, w.Code)
		require.NotEmpty(t, w.Header().Get(GatewayRequestIDHeader))
		require.Contains(t, w.Body.String(), w.Header().Get(GatewayRequestIDHeader))
		require.Contains(t, w.Body.String(), tc.marker)
	}

	req := httptest.NewRequest(http.MethodGet, "/unrelated/not-found", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
	require.Empty(t, w.Header().Get(GatewayRequestIDHeader))
}
