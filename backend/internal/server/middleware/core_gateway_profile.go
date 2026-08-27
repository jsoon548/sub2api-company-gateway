package middleware

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	coreGatewayProtocolAnthropic = "anthropic"
	coreGatewayProtocolOpenAI    = "openai"
	auditPreUpstreamRejectKey    = "core_audit_pre_upstream_rejection"
)

type auditPreUpstreamRejection struct {
	SafeSummary string
}

func markAuditPreUpstreamRejection(c *gin.Context, summary string) {
	c.Set(auditPreUpstreamRejectKey, auditPreUpstreamRejection{SafeSummary: summary})
}

// CoreGatewayExplicitModelAdmission resolves one explicitly requested logical
// model after the existing API Key middleware has authenticated the user. It
// is deliberately profile-based and never inspects User-Agent or API Key data.
func CoreGatewayExplicitModelAdmission(resolver *service.ExplicitModelResolver, profileVersion string) gin.HandlerFunc {
	protocol := protocolForCoreGatewayProfile(profileVersion)
	return func(c *gin.Context) {
		apiKey, ok := GetAPIKeyFromContext(c)
		if !ok || apiKey == nil || apiKey.User == nil || apiKey.GroupID == nil {
			writeCoreGatewayProtocolError(c, protocol, http.StatusUnauthorized, "gateway_authentication_failed", "Authentication failed; request was not sent upstream.")
			return
		}

		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			writeCoreGatewayProtocolError(c, protocol, http.StatusBadRequest, "invalid_request_error", "Failed to read request body.")
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))

		// Preserve existing handler-native validation for malformed JSON or a
		// missing model. Those paths cannot reach an upstream call.
		if len(body) == 0 || !gjson.ValidBytes(body) {
			c.Next()
			return
		}
		model := gjson.GetBytes(body, "model")
		if !model.Exists() || model.Type != gjson.String || strings.TrimSpace(model.String()) == "" {
			c.Next()
			return
		}
		if strings.EqualFold(strings.TrimSpace(model.String()), service.WorkSessionRoutingAuto) {
			markAuditPreUpstreamModelRejection(c, "auto_requires_work_session_boundary")
			writeCoreGatewayProtocolError(c, protocol, http.StatusServiceUnavailable, "gateway_auto_unavailable", "Auto is unavailable; request was not sent upstream.")
			return
		}

		// count_tokens is frozen as HTTP-only. A stream signal cannot silently
		// widen it into an unapproved transport.
		if c.Request.URL.Path == "/v1/messages/count_tokens" && gjson.GetBytes(body, "stream").Bool() {
			writeCoreGatewayProtocolError(c, protocol, http.StatusForbidden, "gateway_entry_not_allowed", "This method, path, transport, or profile is not allowed; request was not sent upstream.")
			return
		}

		if resolver == nil {
			markAuditPreUpstreamModelRejection(c, "explicit_model_resolver_unavailable")
			writeCoreGatewayProtocolError(c, protocol, http.StatusServiceUnavailable, "gateway_model_unavailable", "The explicitly selected model is currently unavailable; request was not sent upstream.")
			return
		}
		resolution, resolveErr := resolver.Resolve(c.Request.Context(), service.ExplicitModelResolveInput{
			AuthenticatedUser: service.ExplicitModelAuthenticatedUser{
				ID: apiKey.User.ID, Status: apiKey.User.Status, Role: apiKey.User.Role,
			},
			Access:                 service.ExplicitModelGroupAccessContext{GroupID: *apiKey.GroupID},
			RequestedLogicalModel:  model.String(),
			ProtocolProfileVersion: profileVersion,
		})
		if resolveErr != nil {
			if errors.Is(resolveErr, service.ErrExplicitModelNotAllowed) {
				markAuditPreUpstreamModelRejection(c, "explicit_model_not_allowed")
				writeCoreGatewayProtocolError(c, protocol, http.StatusForbidden, "gateway_model_not_allowed", "The explicitly selected model is not allowed; request was not sent upstream.")
				return
			}
			markAuditPreUpstreamModelRejection(c, "explicit_model_unavailable")
			writeCoreGatewayProtocolError(c, protocol, http.StatusServiceUnavailable, "gateway_model_unavailable", "The explicitly selected model is currently unavailable; request was not sent upstream.")
			return
		}

		c.Request = c.Request.WithContext(service.WithExplicitModelResolution(c.Request.Context(), resolution))
		c.Next()
	}
}

func markAuditPreUpstreamModelRejection(c *gin.Context, summary string) {
	markAuditPreUpstreamRejection(c, summary)
}

func auditPreUpstreamModelRejectionFromContext(c *gin.Context) (auditPreUpstreamRejection, bool) {
	value, ok := c.Get(auditPreUpstreamRejectKey)
	if !ok {
		return auditPreUpstreamRejection{}, false
	}
	rejection, ok := value.(auditPreUpstreamRejection)
	return rejection, ok && rejection.SafeSummary != ""
}

func CoreGatewayEntryNotAllowed(profileVersion string) gin.HandlerFunc {
	protocol := protocolForCoreGatewayProfile(profileVersion)
	return func(c *gin.Context) {
		writeCoreGatewayProtocolError(c, protocol, http.StatusForbidden, "gateway_entry_not_allowed", "This method, path, transport, or profile is not allowed; request was not sent upstream.")
	}
}

func protocolForCoreGatewayProfile(profileVersion string) string {
	if profileVersion == service.ProtocolProfileAnthropicMessagesV1 {
		return coreGatewayProtocolAnthropic
	}
	return coreGatewayProtocolOpenAI
}

func writeCoreGatewayProtocolError(c *gin.Context, protocol string, status int, code, message string) {
	service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
	requestID, hasRequestID := GatewayRequestIDFromContext(c)
	if hasRequestID {
		c.Header(GatewayRequestIDHeader, requestID.String())
	}
	if protocol == coreGatewayProtocolAnthropic {
		errType := "permission_error"
		if status >= http.StatusInternalServerError {
			errType = "api_error"
		} else if status == http.StatusUnauthorized {
			errType = "authentication_error"
		} else if status == http.StatusBadRequest {
			errType = "invalid_request_error"
		}
		body := gin.H{
			"type":  "error",
			"error": gin.H{"type": errType, "message": message, "code": code},
		}
		if hasRequestID {
			body["gateway_request_id"] = requestID.String()
		}
		c.AbortWithStatusJSON(status, body)
		return
	}

	errType := "invalid_request_error"
	if status >= http.StatusInternalServerError {
		errType = "server_error"
	} else if status == http.StatusUnauthorized {
		errType = "authentication_error"
	}
	body := gin.H{
		"error": gin.H{"message": message, "type": errType, "code": code},
	}
	if hasRequestID {
		body["gateway_request_id"] = requestID.String()
	}
	c.AbortWithStatusJSON(status, body)
}
