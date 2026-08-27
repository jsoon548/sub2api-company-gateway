package routes

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterGatewayRoutes 注册 API 网关路由（Claude/OpenAI/Gemini 兼容）
func RegisterGatewayRoutes(
	r *gin.Engine,
	h *handler.Handlers,
	apiKeyAuth middleware.APIKeyAuthMiddleware,
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	opsService *service.OpsService,
	settingService *service.SettingService,
	explicitModelResolver *service.ExplicitModelResolver,
	auditFoundation *service.AuditFoundationService,
	workSessions *service.WorkSessionService,
	cfg *config.Config,
) {
	bodyLimit := middleware.RequestBodyLimit(cfg.Gateway.MaxBodySize)
	clientRequestID := middleware.ClientRequestID()
	opsErrorLogger := handler.OpsErrorLoggerMiddleware(opsService)
	endpointNorm := handler.InboundEndpointMiddleware()

	// 未分组 Key 拦截中间件（按协议格式区分错误响应）
	requireGroupAnthropic := middleware.RequireGroupAssignment(settingService, middleware.AnthropicErrorWriter)
	requireGroupGoogle := middleware.RequireGroupAssignment(settingService, middleware.GoogleErrorWriter)
	admitAnthropicMessages := middleware.CoreGatewayExplicitModelAdmission(explicitModelResolver, service.ProtocolProfileAnthropicMessagesV1)
	admitOpenAIResponses := middleware.CoreGatewayExplicitModelAdmission(explicitModelResolver, service.ProtocolProfileOpenAIResponsesV1)
	denyAnthropicEntry := middleware.CoreGatewayEntryNotAllowed(service.ProtocolProfileAnthropicMessagesV1)
	denyOpenAIEntry := middleware.CoreGatewayEntryNotAllowed(service.ProtocolProfileOpenAIResponsesV1)
	requestID := middleware.CoreGatewayRequestID()
	admitAnthropicAudit := middleware.CoreGatewayAuditAdmission(auditFoundation, service.ProtocolProfileAnthropicMessagesV1)
	admitOpenAIAudit := middleware.CoreGatewayAuditAdmission(auditFoundation, service.ProtocolProfileOpenAIResponsesV1)
	continueAnthropicAudit := middleware.CoreGatewayAuditContinuity(auditFoundation, service.ProtocolProfileAnthropicMessagesV1)
	continueOpenAIAudit := middleware.CoreGatewayAuditContinuity(auditFoundation, service.ProtocolProfileOpenAIResponsesV1)
	associateAnthropicWorkSession := middleware.CoreGatewayWorkSessionAdmissionWithResolver(workSessions, explicitModelResolver, service.ProtocolProfileAnthropicMessagesV1)
	associateOpenAIWorkSession := middleware.CoreGatewayWorkSessionAdmissionWithResolver(workSessions, explicitModelResolver, service.ProtocolProfileOpenAIResponsesV1)
	strictAnthropicMessages := middleware.CoreGatewayStrictEntryProfile(service.ProtocolProfileAnthropicMessagesV1, middleware.CoreGatewayEntryShape{Path: "/v1/messages", AllowHTTP: true, AllowSSE: true, RequireAPI: middleware.CoreGatewayAPIAnthropicVersion})
	strictAnthropicCountTokens := middleware.CoreGatewayStrictEntryProfile(service.ProtocolProfileAnthropicMessagesV1, middleware.CoreGatewayEntryShape{Path: "/v1/messages/count_tokens", AllowHTTP: true, RequireAPI: middleware.CoreGatewayAPIAnthropicVersion})
	strictV1OpenAIResponses := middleware.CoreGatewayStrictEntryProfile(service.ProtocolProfileOpenAIResponsesV1, middleware.CoreGatewayEntryShape{Path: "/v1/responses", AllowHTTP: true, AllowSSE: true})
	strictOpenAIResponses := middleware.CoreGatewayStrictEntryProfile(service.ProtocolProfileOpenAIResponsesV1, middleware.CoreGatewayEntryShape{Path: "/responses", AllowHTTP: true, AllowSSE: true})
	strictV1OpenAICompact := middleware.CoreGatewayStrictEntryProfile(service.ProtocolProfileOpenAIResponsesV1, middleware.CoreGatewayEntryShape{Path: "/v1/responses/compact", AllowHTTP: true, AllowSSE: true})
	strictOpenAICompact := middleware.CoreGatewayStrictEntryProfile(service.ProtocolProfileOpenAIResponsesV1, middleware.CoreGatewayEntryShape{Path: "/responses/compact", AllowHTTP: true, AllowSSE: true})
	requireOpenAICompact := middleware.CoreGatewayRequireResolvedPlatform(service.PlatformOpenAI)

	isOpenAIResponsesCompatibleGatewayPlatform := func(c *gin.Context) bool {
		switch getGroupPlatform(c) {
		case service.PlatformOpenAI, service.PlatformGrok:
			return true
		default:
			return false
		}
	}
	isOpenAIGatewayPlatform := func(c *gin.Context) bool {
		return getGroupPlatform(c) == service.PlatformOpenAI
	}
	imagesHandler := func(c *gin.Context) {
		switch getGroupPlatform(c) {
		case service.PlatformOpenAI:
			h.OpenAIGateway.Images(c)
		case service.PlatformGrok:
			h.OpenAIGateway.GrokImages(c)
		default:
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{
					"type":    "not_found_error",
					"message": "Images API is not supported for this platform",
				},
			})
		}
	}
	videoGenerationHandler := func(c *gin.Context) {
		if getGroupPlatform(c) == service.PlatformGrok {
			h.OpenAIGateway.GrokVideoGeneration(c)
			return
		}
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found_error",
				"message": "Videos API is not supported for this platform",
			},
		})
	}
	videoStatusHandler := func(c *gin.Context) {
		if getGroupPlatform(c) == service.PlatformGrok {
			h.OpenAIGateway.GrokVideoStatus(c)
			return
		}
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"type":    "not_found_error",
				"message": "Videos API is not supported for this platform",
			},
		})
	}
	// API网关（Claude API兼容）
	gateway := r.Group("/v1")
	gateway.Use(bodyLimit)
	gateway.Use(clientRequestID)
	gateway.Use(opsErrorLogger)
	gateway.Use(endpointNorm)
	gateway.Use(requestID)
	gateway.Use(gin.HandlerFunc(apiKeyAuth))
	gateway.Use(requireGroupAnthropic)
	{
		// /v1/messages: auto-route based on group platform
		gateway.POST("/messages", admitAnthropicAudit, continueAnthropicAudit, strictAnthropicMessages, associateAnthropicWorkSession, admitAnthropicMessages, func(c *gin.Context) {
			if isOpenAIResponsesCompatibleGatewayPlatform(c) {
				h.OpenAIGateway.Messages(c)
				return
			}
			h.Gateway.Messages(c)
		})
		// /v1/messages/count_tokens: OpenAI uses Anthropic-compat bridge; other
		// OpenAI-compatible platforms keep the prior unsupported response.
		gateway.POST("/messages/count_tokens", admitAnthropicAudit, continueAnthropicAudit, strictAnthropicCountTokens, associateAnthropicWorkSession, admitAnthropicMessages, func(c *gin.Context) {
			if isOpenAIGatewayPlatform(c) {
				h.OpenAIGateway.CountTokens(c)
				return
			}
			if isOpenAIResponsesCompatibleGatewayPlatform(c) {
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
				c.JSON(http.StatusNotFound, gin.H{
					"type": "error",
					"error": gin.H{
						"type":    "not_found_error",
						"message": "Token counting is not supported for this platform",
					},
				})
				return
			}
			h.Gateway.CountTokens(c)
		})
		gateway.POST("/messages/unknown", denyAnthropicEntry)
		// Codex CLI / Codex app refresh their model picker from the provider's
		// /models endpoint with a client_version query and expect the ChatGPT
		// Codex manifest format; other clients keep the OpenAI-style list.
		gateway.GET("/models", func(c *gin.Context) {
			if isOpenAIGatewayPlatform(c) && c.Query("client_version") != "" {
				h.OpenAIGateway.CodexModels(c)
				return
			}
			h.Gateway.Models(c)
		})
		gateway.GET("/usage", h.Gateway.Usage)
		// OpenAI Responses API: auto-route based on group platform
		gateway.POST("/responses", admitOpenAIAudit, continueOpenAIAudit, strictV1OpenAIResponses, associateOpenAIWorkSession, admitOpenAIResponses, func(c *gin.Context) {
			if isOpenAIResponsesCompatibleGatewayPlatform(c) {
				h.OpenAIGateway.Responses(c)
				return
			}
			h.Gateway.Responses(c)
		})
		gateway.POST("/responses/compact", admitOpenAIAudit, continueOpenAIAudit, strictV1OpenAICompact, associateOpenAIWorkSession, admitOpenAIResponses, requireOpenAICompact, h.OpenAIGateway.Responses)
		gateway.POST("/responses/unknown", denyOpenAIEntry)
		gateway.GET("/responses", denyOpenAIEntry)
		// OpenAI Chat Completions API: auto-route based on group platform
		gateway.POST("/chat/completions", denyOpenAIEntry)
		gateway.POST("/embeddings", func(c *gin.Context) {
			if getGroupPlatform(c) != service.PlatformOpenAI {
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
				c.JSON(http.StatusNotFound, gin.H{
					"error": gin.H{
						"type":    "not_found_error",
						"message": "Embeddings API is not supported for this platform",
					},
				})
				return
			}
			h.OpenAIGateway.Embeddings(c)
		})
		gateway.POST("/images/generations", imagesHandler)
		gateway.POST("/images/edits", imagesHandler)
		gateway.POST("/images/batches", h.BatchImage.Submit)
		gateway.GET("/images/batches", h.BatchImage.List)
		gateway.GET("/images/batches/models", h.BatchImage.Models)
		gateway.GET("/images/batches/:id", h.BatchImage.Get)
		gateway.GET("/images/batches/:id/items", h.BatchImage.Items)
		gateway.GET("/images/batches/:id/items/:custom_id/content", h.BatchImage.ItemContent)
		gateway.GET("/images/batches/:id/download", h.BatchImage.Download)
		gateway.POST("/images/batches/:id/cancel", h.BatchImage.Cancel)
		gateway.DELETE("/images/batches/:id", h.BatchImage.DeleteRecord)
		gateway.DELETE("/images/batches/:id/outputs", h.BatchImage.DeleteOutputs)
		gateway.POST("/videos/generations", videoGenerationHandler)
		gateway.GET("/videos/:request_id", videoStatusHandler)
	}

	// Gemini 原生 API 兼容层（Gemini SDK/CLI 直连）
	gemini := r.Group("/v1beta")
	gemini.Use(bodyLimit)
	gemini.Use(clientRequestID)
	gemini.Use(opsErrorLogger)
	gemini.Use(endpointNorm)
	gemini.Use(middleware.APIKeyAuthWithSubscriptionGoogle(apiKeyService, subscriptionService, cfg))
	gemini.Use(requireGroupGoogle)
	{
		gemini.GET("/models", h.Gateway.GeminiV1BetaListModels)
		gemini.GET("/models/:model", h.Gateway.GeminiV1BetaGetModel)
		// Gin treats ":" as a param marker, but Gemini uses "{model}:{action}" in the same segment.
		gemini.POST("/models/*modelAction", h.Gateway.GeminiV1BetaModels)
	}

	// OpenAI Responses API（不带v1前缀的别名）— auto-route based on group platform
	responsesHandler := func(c *gin.Context) {
		if isOpenAIResponsesCompatibleGatewayPlatform(c) {
			h.OpenAIGateway.Responses(c)
			return
		}
		h.Gateway.Responses(c)
	}
	r.POST("/responses", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, requestID, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, admitOpenAIAudit, continueOpenAIAudit, strictOpenAIResponses, associateOpenAIWorkSession, admitOpenAIResponses, responsesHandler)
	r.POST("/responses/compact", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, requestID, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, admitOpenAIAudit, continueOpenAIAudit, strictOpenAICompact, associateOpenAIWorkSession, admitOpenAIResponses, requireOpenAICompact, h.OpenAIGateway.Responses)
	r.POST("/responses/unknown", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, requestID, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, denyOpenAIEntry)
	r.GET("/responses", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, requestID, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, denyOpenAIEntry)
	r.POST("/messages", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, requestID, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, denyAnthropicEntry)
	codexDirect := r.Group("/backend-api/codex")
	codexDirect.Use(bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, requestID, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic)
	{
		codexDirect.POST("/responses", denyOpenAIEntry)
		codexDirect.GET("/responses", denyOpenAIEntry)
		codexDirect.POST("/responses/compact", denyOpenAIEntry)
		codexDirect.GET("/models", h.OpenAIGateway.CodexModels)
	}
	// OpenAI Chat Completions API（不带v1前缀的别名）— auto-route based on group platform
	r.POST("/chat/completions", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, requestID, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, denyOpenAIEntry)
	r.POST("/embeddings", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, func(c *gin.Context) {
		if getGroupPlatform(c) != service.PlatformOpenAI {
			service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
			c.JSON(http.StatusNotFound, gin.H{
				"error": gin.H{
					"type":    "not_found_error",
					"message": "Embeddings API is not supported for this platform",
				},
			})
			return
		}
		h.OpenAIGateway.Embeddings(c)
	})
	r.POST("/images/generations", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, imagesHandler)
	r.POST("/images/edits", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, imagesHandler)
	r.POST("/videos/generations", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, videoGenerationHandler)
	r.GET("/videos/:request_id", bodyLimit, clientRequestID, opsErrorLogger, endpointNorm, gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, videoStatusHandler)

	// Unknown Gateway aliases/subpaths and wrong methods stay default-deny with
	// protocol-native errors. NoRoute deliberately captures no rejected body.
	r.NoRoute(middleware.CoreGatewayNoRouteDefaultDeny())

	// Antigravity 模型列表
	r.GET("/antigravity/models", gin.HandlerFunc(apiKeyAuth), requireGroupAnthropic, h.Gateway.AntigravityModels)

	// Antigravity 专用路由（仅使用 antigravity 账户，不混合调度）
	antigravityV1 := r.Group("/antigravity/v1")
	antigravityV1.Use(bodyLimit)
	antigravityV1.Use(clientRequestID)
	antigravityV1.Use(opsErrorLogger)
	antigravityV1.Use(endpointNorm)
	antigravityV1.Use(middleware.ForcePlatform(service.PlatformAntigravity))
	antigravityV1.Use(gin.HandlerFunc(apiKeyAuth))
	antigravityV1.Use(requireGroupAnthropic)
	{
		antigravityV1.POST("/messages", h.Gateway.Messages)
		antigravityV1.POST("/messages/count_tokens", h.Gateway.CountTokens)
		antigravityV1.GET("/models", h.Gateway.AntigravityModels)
		antigravityV1.GET("/usage", h.Gateway.Usage)
	}

	antigravityV1Beta := r.Group("/antigravity/v1beta")
	antigravityV1Beta.Use(bodyLimit)
	antigravityV1Beta.Use(clientRequestID)
	antigravityV1Beta.Use(opsErrorLogger)
	antigravityV1Beta.Use(endpointNorm)
	antigravityV1Beta.Use(middleware.ForcePlatform(service.PlatformAntigravity))
	antigravityV1Beta.Use(middleware.APIKeyAuthWithSubscriptionGoogle(apiKeyService, subscriptionService, cfg))
	antigravityV1Beta.Use(requireGroupGoogle)
	{
		antigravityV1Beta.GET("/models", h.Gateway.GeminiV1BetaListModels)
		antigravityV1Beta.GET("/models/:model", h.Gateway.GeminiV1BetaGetModel)
		antigravityV1Beta.POST("/models/*modelAction", h.Gateway.GeminiV1BetaModels)
	}

}

// getGroupPlatform extracts the group platform from the API Key stored in context.
func getGroupPlatform(c *gin.Context) string {
	apiKey, ok := middleware.GetAPIKeyFromContext(c)
	if !ok || apiKey.Group == nil {
		return ""
	}
	return apiKey.Group.Platform
}
