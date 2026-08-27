package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type GovernanceHandler struct {
	governance *service.SuperAdminTransferService
}

func NewGovernanceHandler(governance *service.SuperAdminTransferService) *GovernanceHandler {
	return &GovernanceHandler{governance: governance}
}

type transferSuperAdminRequest struct {
	TargetUserID        int64  `json:"target_user_id" binding:"required,gt=0"`
	ExpectedSeatVersion int64  `json:"expected_seat_version" binding:"required,gt=0"`
	Reason              string `json:"reason" binding:"required,max=512"`
}

type userLifecycleRequest struct {
	Reason string `json:"reason" binding:"required,max=512"`
}

func governanceActor(c *gin.Context) (middleware.AuthSubject, string, string, bool) {
	actor, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "Named administrator session required")
		return actor, "", "", false
	}
	role, ok := middleware.GetUserRoleFromContext(c)
	if !ok {
		response.Unauthorized(c, "Named administrator session required")
		return actor, "", "", false
	}
	return actor, role, c.GetString("auth_method"), true
}

func (h *GovernanceHandler) TransferSuperAdmin(c *gin.Context) {
	var req transferSuperAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Reason) == "" {
		response.BadRequest(c, "Invalid request")
		return
	}
	actor, role, authMethod, ok := governanceActor(c)
	if !ok {
		return
	}
	result, err := h.governance.Transfer(c.Request.Context(), role, authMethod, service.SuperAdminTransferInput{
		OperationID: uuid.New(), ActorUserID: actor.UserID, TargetUserID: req.TargetUserID,
		ExpectedSeatVersion: req.ExpectedSeatVersion, Reason: strings.TrimSpace(req.Reason),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *GovernanceHandler) GetSuperAdminSeat(c *gin.Context) {
	result, err := h.governance.CurrentSeat(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *GovernanceHandler) DeactivateUser(c *gin.Context) { h.changeLifecycle(c, false) }
func (h *GovernanceHandler) ReactivateUser(c *gin.Context) { h.changeLifecycle(c, true) }

func (h *GovernanceHandler) changeLifecycle(c *gin.Context, reactivate bool) {
	targetID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid user ID")
		return
	}
	var req userLifecycleRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Reason) == "" {
		response.BadRequest(c, "Invalid request")
		return
	}
	actor, role, authMethod, ok := governanceActor(c)
	if !ok {
		return
	}
	input := service.UserLifecycleInput{OperationID: uuid.New(), ActorUserID: actor.UserID, TargetUserID: targetID, Reason: strings.TrimSpace(req.Reason)}
	if reactivate {
		err = h.governance.ReactivateUser(c.Request.Context(), role, authMethod, input)
	} else {
		err = h.governance.DeactivateUser(c.Request.Context(), role, authMethod, input)
	}
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	status := service.StatusDisabled
	if reactivate {
		status = service.StatusActive
	}
	response.Success(c, gin.H{"user_id": targetID, "status": status})
}
