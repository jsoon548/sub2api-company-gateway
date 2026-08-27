package admin

import (
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type WorkSessionHandler struct {
	workSessions *service.WorkSessionService
}

func NewWorkSessionHandler(workSessions *service.WorkSessionService) *WorkSessionHandler {
	return &WorkSessionHandler{workSessions: workSessions}
}

func (h *WorkSessionHandler) GetManagementState(c *gin.Context) {
	state, err := h.workSessions.GetManagementState(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, state)
}

func (h *WorkSessionHandler) ReplaceManagementConfig(c *gin.Context) {
	var input service.WorkSessionManagementUpdate
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid Work Session/Auto configuration")
		return
	}
	state, err := h.workSessions.ReplaceManagementConfig(c.Request.Context(), input)
	if errors.Is(err, service.ErrWorkSessionInvalid) {
		response.BadRequest(c, "Invalid Work Session/Auto configuration")
		return
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, state)
}

type emergencyModelDisableRequest struct {
	Disabled bool `json:"disabled"`
}

func (h *WorkSessionHandler) SetEmergencyModelDisable(c *gin.Context) {
	model := strings.TrimSpace(c.Param("logical_model"))
	var input emergencyModelDisableRequest
	if model == "" || c.ShouldBindJSON(&input) != nil {
		response.BadRequest(c, "Invalid model emergency state")
		return
	}
	err := h.workSessions.SetEmergencyDisabled(c.Request.Context(), model, input.Disabled)
	if errors.Is(err, service.ErrModelCatalogNotFound) {
		response.NotFound(c, "Model catalog entry not found")
		return
	}
	if errors.Is(err, service.ErrWorkSessionInvalid) {
		response.BadRequest(c, "Invalid model emergency state")
		return
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	state, err := h.workSessions.GetManagementState(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, state)
}
