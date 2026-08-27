package admin

import (
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type AuditManagementHandler struct {
	audit *service.AuditManagementService
}

func NewAuditManagementHandler(audit *service.AuditManagementService) *AuditManagementHandler {
	return &AuditManagementHandler{audit: audit}
}

func (h *AuditManagementHandler) ListMetadata(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	filter := service.AuditMetadataFilter{
		Employee:       c.Query("employee"),
		Protocol:       c.Query("protocol"),
		Model:          c.Query("model"),
		RequestOutcome: c.Query("outcome"),
		ContentState:   c.Query("content_state"),
		Page:           page,
		PageSize:       pageSize,
	}
	if raw := strings.TrimSpace(c.Query("from")); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			response.BadRequest(c, "Invalid from time")
			return
		}
		filter.From = &value
	}
	if raw := strings.TrimSpace(c.Query("to")); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			response.BadRequest(c, "Invalid to time")
			return
		}
		filter.To = &value
	}
	if raw := strings.TrimSpace(c.Query("gateway_request_id")); raw != "" {
		value, err := uuid.Parse(raw)
		if err != nil {
			response.BadRequest(c, "Invalid Gateway Request ID")
			return
		}
		filter.GatewayRequestID = &value
	}
	result, err := h.audit.ListMetadata(c.Request.Context(), filter)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func parseGatewayUsageFilter(c *gin.Context) (service.GatewayUsageFilter, bool) {
	page, pageSize := response.ParsePagination(c)
	filter := service.GatewayUsageFilter{
		Employee: c.Query("employee"), Profile: c.Query("profile"),
		Protocol: c.Query("protocol"), Model: c.Query("model"),
		Result: c.Query("result"), RequestOutcome: c.Query("outcome"),
		ContentState: c.Query("content_state"),
		Page:         page, PageSize: pageSize,
	}
	if raw := strings.TrimSpace(c.Query("from")); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			response.BadRequest(c, "Invalid from time")
			return service.GatewayUsageFilter{}, false
		}
		filter.From = &value
	}
	if raw := strings.TrimSpace(c.Query("to")); raw != "" {
		value, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			response.BadRequest(c, "Invalid to time")
			return service.GatewayUsageFilter{}, false
		}
		filter.To = &value
	}
	if raw := strings.TrimSpace(c.Query("gateway_request_id")); raw != "" {
		value, err := uuid.Parse(raw)
		if err != nil {
			response.BadRequest(c, "Invalid Gateway Request ID")
			return service.GatewayUsageFilter{}, false
		}
		filter.GatewayRequestID = &value
	}
	return filter, true
}

func (h *AuditManagementHandler) ListGatewayUsage(c *gin.Context) {
	filter, ok := parseGatewayUsageFilter(c)
	if !ok {
		return
	}
	result, err := h.audit.ListGatewayUsage(c.Request.Context(), filter)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *AuditManagementHandler) SummarizeGatewayUsage(c *gin.Context) {
	filter, ok := parseGatewayUsageFilter(c)
	if !ok {
		return
	}
	result, err := h.audit.SummarizeGatewayUsage(c.Request.Context(), filter, c.Query("group_by"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *AuditManagementHandler) GetGatewayUsage(c *gin.Context) {
	gatewayRequestID, err := uuid.Parse(c.Param("gateway_request_id"))
	if err != nil {
		response.BadRequest(c, "Invalid Gateway Request ID")
		return
	}
	result, err := h.audit.GetGatewayUsage(c.Request.Context(), gatewayRequestID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *AuditManagementHandler) Disclose(c *gin.Context) {
	interactionID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		response.BadRequest(c, "Invalid audit interaction ID")
		return
	}
	actor, role, authMethod, ok := governanceActor(c)
	if !ok {
		return
	}
	result, err := h.audit.Disclose(c.Request.Context(), service.AuditDisclosureInput{
		InteractionID: interactionID,
		Actor: service.AuditDisclosureActor{
			UserID: actor.UserID, SessionVersion: actor.SessionVersion,
			SessionExpiresAt: actor.SessionExpiresAt,
			Role:             role, AuthMethod: authMethod,
		},
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
