package middleware

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const workSessionContextKey = "core_gateway_work_session"

// CoreGatewayWorkSessionAdmission preserves the focused association-test call
// shape. Production Auto routing uses the resolver-aware variant.
func CoreGatewayWorkSessionAdmission(workSessions *service.WorkSessionService, profileVersion string) gin.HandlerFunc {
	return CoreGatewayWorkSessionAdmissionWithResolver(workSessions, nil, profileVersion)
}

// CoreGatewayWorkSessionAdmissionWithResolver associates the Work Session,
// admits Auto, selects one logical model, and then lets the next middleware run
// the selected logical model through the Explicit Model Resolver again.
func CoreGatewayWorkSessionAdmissionWithResolver(workSessions *service.WorkSessionService, resolver *service.ExplicitModelResolver, profileVersion string) gin.HandlerFunc {
	protocol := protocolForCoreGatewayProfile(profileVersion)
	return func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			markAuditPreUpstreamModelRejection(c, "work_session_request_unreadable")
			writeCoreGatewayProtocolError(c, protocol, http.StatusBadRequest, "invalid_request_error", "Failed to read request body.")
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		requestedModel := ""
		if model := gjson.GetBytes(body, "model"); model.Type == gjson.String {
			requestedModel = strings.TrimSpace(model.String())
		}
		isAuto := strings.EqualFold(requestedModel, service.WorkSessionRoutingAuto)
		apiKey, authenticated := GetAPIKeyFromContext(c)
		gatewayID, hasGatewayID := GatewayRequestIDFromContext(c)
		if !authenticated || apiKey == nil || apiKey.User == nil || apiKey.GroupID == nil || !hasGatewayID {
			if isAuto {
				markAuditPreUpstreamModelRejection(c, "auto_authentication_context_unavailable")
				writeCoreGatewayProtocolError(c, protocol, http.StatusServiceUnavailable, "gateway_auto_unavailable", "Auto is unavailable; request was not sent upstream.")
				return
			}
			c.Next()
			return
		}
		if workSessions == nil {
			if isAuto {
				markAuditPreUpstreamModelRejection(c, "auto_work_session_service_unavailable")
				writeCoreGatewayProtocolError(c, protocol, http.StatusServiceUnavailable, "gateway_auto_unavailable", "Auto is unavailable; request was not sent upstream.")
				return
			}
			c.Next()
			return
		}

		signal := service.ExtractWorkSessionSignal(profileVersion, c.Request.Header)
		record, associateErr := workSessions.AssociateRequest(c.Request.Context(), service.WorkSessionAssociateInput{
			EmployeeUserID: apiKey.User.ID, ProfileVersion: profileVersion, Signal: signal,
			RequestedModel: requestedModel, GatewayRequestID: gatewayID,
		})
		if associateErr != nil {
			if isAuto {
				markAuditPreUpstreamModelRejection(c, "auto_reliable_work_session_unavailable")
				writeCoreGatewayProtocolError(c, protocol, http.StatusServiceUnavailable, "gateway_auto_unavailable", "Auto requires an available reliable Work Session; request was not sent upstream.")
				return
			}
			// Reliable Work Session is an independent capability. Explicit model
			// requests preserve their explicit-model contract behavior when the key or schema is unavailable.
			c.Next()
			return
		}
		c.Set(workSessionContextKey, record)
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.WorkSessionID, record.ID))

		if !isAuto {
			c.Next()
			return
		}
		decision, autoErr := workSessions.EvaluateAutoBoundary(c.Request.Context(), requestedModel, apiKey.User.ID, *apiKey.GroupID, record)
		if autoErr != nil {
			status := http.StatusForbidden
			code := "gateway_auto_not_allowed"
			if errors.Is(autoErr, service.ErrWorkSessionUnavailable) || errors.Is(autoErr, service.ErrWorkSessionSchema) {
				status = http.StatusServiceUnavailable
				code = "gateway_auto_unavailable"
			}
			markAuditPreUpstreamModelRejection(c, "auto_"+decision.Reason)
			writeCoreGatewayProtocolError(c, protocol, status, code, "Auto is not available for this request; request was not sent upstream.")
			return
		}
		if !decision.EnterAuto {
			markAuditPreUpstreamModelRejection(c, "auto_boundary_not_entered")
			writeCoreGatewayProtocolError(c, protocol, http.StatusForbidden, "gateway_auto_not_allowed", "Auto is not available for this request; request was not sent upstream.")
			return
		}

		result, routeErr := workSessions.RouteAuto(c.Request.Context(), service.AutoRouteInput{
			GatewayRequestID: gatewayID,
			Session:          record,
			Body:             body,
			ProfileVersion:   profileVersion,
			User: service.ExplicitModelAuthenticatedUser{
				ID: apiKey.User.ID, Status: apiKey.User.Status, Role: apiKey.User.Role,
			},
			Access:   service.ExplicitModelGroupAccessContext{GroupID: *apiKey.GroupID},
			Resolver: resolver,
		})
		if routeErr != nil {
			reason := "auto_routing_unavailable"
			code := "gateway_auto_routing_unavailable"
			message := "Auto routing is unavailable; request was not sent upstream."
			if errors.Is(routeErr, service.ErrAutoNoCandidate) {
				reason = "auto_no_eligible_candidate"
				code = "gateway_auto_model_unavailable"
				message = "No eligible Auto model is available at the required tier or above; request was not sent upstream."
			}
			markAuditPreUpstreamModelRejection(c, reason)
			writeCoreGatewayProtocolError(c, protocol, http.StatusServiceUnavailable, code, message)
			return
		}

		c.Request.Body = io.NopCloser(bytes.NewReader(result.RewrittenBody))
		c.Request.ContentLength = int64(len(result.RewrittenBody))
		c.Request = c.Request.WithContext(service.WithAutoRouteRuntime(c.Request.Context(), result.Runtime))
		service.ApplyAutoRouteResponseHeaders(c.Writer.Header(), c.Request.Context())
		c.Next()

		finalizeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = workSessions.FinalizeAutoRoute(finalizeCtx, result.Runtime)
	}
}

func WorkSessionFromContext(c *gin.Context) (service.WorkSessionRecord, bool) {
	value, ok := c.Get(workSessionContextKey)
	if !ok {
		return service.WorkSessionRecord{}, false
	}
	record, ok := value.(service.WorkSessionRecord)
	return record, ok && record.ID != uuid.Nil
}
