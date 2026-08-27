//go:build unit

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

type coreGatewayCatalogStub struct {
	entry *service.ExplicitModelApprovalSnapshot
}

func (s coreGatewayCatalogStub) FindApprovedExplicitModel(_ context.Context, _ service.ExplicitModelResolveInput) (*service.ExplicitModelApprovalSnapshot, error) {
	if s.entry == nil {
		return nil, nil
	}
	entry := *s.entry
	entry.SchedulableAccountScope = append([]int64(nil), s.entry.SchedulableAccountScope...)
	return &entry, nil
}

func TestCoreGatewayAdmissionAPIKeySecretDoesNotChooseModelOrPlatform(t *testing.T) {
	gin.SetMode(gin.TestMode)
	entry := &service.ExplicitModelApprovalSnapshot{
		EntryID: "company-coder", GroupID: 7, ChannelID: 8, LogicalModel: "company-coder",
		Platform: service.PlatformOpenAI, ResolvedProviderModel: "gpt-5.6-codex",
		SchedulableAccountScope: []int64{11}, ConfigurationVersion: 1,
	}
	resolver := service.NewExplicitModelResolver(coreGatewayCatalogStub{entry: entry})

	for _, keySecret := range []string{"synthetic-key-alpha", "synthetic-key-beta"} {
		t.Run(keySecret, func(t *testing.T) {
			router := gin.New()
			router.POST("/v1/messages", func(c *gin.Context) {
				groupID := int64(7)
				c.Set(string(ContextKeyAPIKey), &service.APIKey{
					Key: keySecret, GroupID: &groupID,
					User: &service.User{ID: 42, Status: service.StatusActive, Role: service.RoleUser},
				})
				c.Next()
			}, CoreGatewayExplicitModelAdmission(resolver, service.ProtocolProfileAnthropicMessagesV1), func(c *gin.Context) {
				resolution, ok := service.ExplicitModelResolutionFromContext(c.Request.Context())
				require.True(t, ok)
				require.Equal(t, "company-coder", resolution.RequestedLogicalModel)
				require.Equal(t, "gpt-5.6-codex", resolution.ResolvedProviderModel)
				require.Equal(t, service.PlatformOpenAI, resolution.Platform)
				c.Status(http.StatusNoContent)
			})

			req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"company-coder","stream":false}`))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusNoContent, w.Code)
		})
	}
}

func TestCoreGatewayAdmissionSameExistingAPIKeyAcrossFrozenProfiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	entry := &service.ExplicitModelApprovalSnapshot{
		EntryID: "company-coder", GroupID: 7, ChannelID: 8, LogicalModel: "company-coder",
		Platform: service.PlatformOpenAI, ResolvedProviderModel: "gpt-5.6-codex",
		SchedulableAccountScope: []int64{11}, ConfigurationVersion: 1,
	}
	resolver := service.NewExplicitModelResolver(coreGatewayCatalogStub{entry: entry})
	groupID := int64(7)
	existingKey := &service.APIKey{
		ID: 91, UserID: 42, Key: "synthetic-existing-user-key", Status: service.StatusAPIKeyActive,
		GroupID: &groupID,
		User:    &service.User{ID: 42, Status: service.StatusActive, Role: service.RoleUser},
	}

	router := gin.New()
	authenticate := func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), existingKey)
		c.Next()
	}
	upstreamCalls := map[string]int{}
	forward := func(profile string) gin.HandlerFunc {
		return func(c *gin.Context) {
			resolution, ok := service.ExplicitModelResolutionFromContext(c.Request.Context())
			require.True(t, ok)
			require.Equal(t, "company-coder", resolution.RequestedLogicalModel)
			require.Equal(t, "gpt-5.6-codex", resolution.ResolvedProviderModel)
			upstreamCalls[profile]++
			c.Status(http.StatusNoContent)
		}
	}
	router.POST("/v1/messages", authenticate, CoreGatewayExplicitModelAdmission(resolver, service.ProtocolProfileAnthropicMessagesV1), forward(service.ProtocolProfileAnthropicMessagesV1))
	router.POST("/v1/responses", authenticate, CoreGatewayExplicitModelAdmission(resolver, service.ProtocolProfileOpenAIResponsesV1), forward(service.ProtocolProfileOpenAIResponsesV1))

	for _, path := range []string{"/v1/messages", "/v1/responses"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"company-coder","stream":false}`))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusNoContent, w.Code)
	}
	require.Equal(t, 1, upstreamCalls[service.ProtocolProfileAnthropicMessagesV1])
	require.Equal(t, 1, upstreamCalls[service.ProtocolProfileOpenAIResponsesV1])
}

func TestCoreGatewayAdmissionRejectsBeforeUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name       string
		profile    string
		path       string
		entry      *service.ExplicitModelApprovalSnapshot
		wantStatus int
		wantCode   string
	}{
		{name: "anthropic not allowed", profile: service.ProtocolProfileAnthropicMessagesV1, path: "/v1/messages", wantStatus: http.StatusForbidden, wantCode: "gateway_model_not_allowed"},
		{name: "openai unavailable", profile: service.ProtocolProfileOpenAIResponsesV1, path: "/v1/responses", entry: &service.ExplicitModelApprovalSnapshot{EntryID: "x", GroupID: 7, LogicalModel: "company-coder", Platform: service.PlatformOpenAI, ResolvedProviderModel: "gpt-5.6-codex"}, wantStatus: http.StatusServiceUnavailable, wantCode: "gateway_model_unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstreamCount := 0
			router := gin.New()
			router.POST(tt.path, func(c *gin.Context) {
				groupID := int64(7)
				c.Set(string(ContextKeyAPIKey), &service.APIKey{GroupID: &groupID, User: &service.User{ID: 42, Status: service.StatusActive, Role: service.RoleUser}})
				c.Next()
			}, CoreGatewayExplicitModelAdmission(service.NewExplicitModelResolver(coreGatewayCatalogStub{entry: tt.entry}), tt.profile), func(c *gin.Context) {
				upstreamCount++
			})

			req := httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(`{"model":"company-coder"}`))
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, tt.wantStatus, w.Code)
			require.Contains(t, w.Body.String(), tt.wantCode)
			require.Zero(t, upstreamCount)
		})
	}
}

func TestCoreGatewayCountTokensRejectsSSETransport(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstreamCount := 0
	router := gin.New()
	router.POST("/v1/messages/count_tokens", func(c *gin.Context) {
		groupID := int64(7)
		c.Set(string(ContextKeyAPIKey), &service.APIKey{GroupID: &groupID, User: &service.User{ID: 42, Status: service.StatusActive, Role: service.RoleUser}})
		c.Next()
	}, CoreGatewayExplicitModelAdmission(service.NewExplicitModelResolver(coreGatewayCatalogStub{}), service.ProtocolProfileAnthropicMessagesV1), func(c *gin.Context) {
		upstreamCount++
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{"model":"company-coder","stream":true}`))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "gateway_entry_not_allowed")
	require.Zero(t, upstreamCount)
}
