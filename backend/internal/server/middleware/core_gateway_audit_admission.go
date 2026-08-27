package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const (
	GatewayRequestIDHeader  = "X-Gateway-Request-ID"
	gatewayRequestIDKey     = "core_gateway_request_id"
	auditInteractionIDKey   = "core_audit_interaction_id"
	auditAdmissionResultKey = "core_audit_admission_result"
	auditTransportKey       = "core_audit_transport"
)

var auditAllowedRequestHeaders = map[string]struct{}{
	"Accept":            {},
	"Content-Type":      {},
	"Anthropic-Version": {},
	"Anthropic-Beta":    {},
	"OpenAI-Beta":       {},
}

type auditCapturedHeader struct {
	Name   string   `json:"name"`
	Values []string `json:"values"`
}

type auditRequestEnvelope struct {
	Version    string                `json:"version"`
	Method     string                `json:"method"`
	RequestURI string                `json:"request_uri"`
	Headers    []auditCapturedHeader `json:"headers"`
	Body       []byte                `json:"body"`
}

// CoreGatewayRequestID replaces any client-supplied value with a Gateway-owned UUID.
func CoreGatewayRequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := uuid.New()
		c.Set(gatewayRequestIDKey, id)
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.GatewayRequestID, id))
		c.Request.Header.Set(GatewayRequestIDHeader, id.String())
		c.Header(GatewayRequestIDHeader, id.String())
		c.Next()
	}
}

func GatewayRequestIDFromContext(c *gin.Context) (uuid.UUID, bool) {
	value, ok := c.Get(gatewayRequestIDKey)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := value.(uuid.UUID)
	return id, ok && id != uuid.Nil
}

// CoreGatewayAuditAdmission runs after API-key authentication and before model
// resolution. It restores the body for downstream code only after the exact
// request envelope is encrypted and atomically committed.
func CoreGatewayAuditAdmission(audit *service.AuditFoundationService, profileVersion string) gin.HandlerFunc {
	protocol := protocolForCoreGatewayProfile(profileVersion)
	return func(c *gin.Context) {
		gatewayID, ok := GatewayRequestIDFromContext(c)
		if !ok {
			writeCoreGatewayProtocolError(c, protocol, http.StatusServiceUnavailable, "gateway_audit_unavailable", "Audit service is unavailable; request was not sent upstream.")
			return
		}
		apiKey, ok := GetAPIKeyFromContext(c)
		if !ok || apiKey == nil || apiKey.User == nil {
			writeCoreGatewayProtocolError(c, protocol, http.StatusUnauthorized, "gateway_authentication_failed", "Authentication failed; request was not sent upstream.")
			return
		}
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			writeCoreGatewayProtocolError(c, protocol, http.StatusServiceUnavailable, "gateway_audit_unavailable", "Audit service is unavailable; request was not sent upstream.")
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		envelope, err := encodeAuditRequestEnvelope(c.Request, body)
		if err != nil {
			writeCoreGatewayProtocolError(c, protocol, http.StatusServiceUnavailable, "gateway_audit_unavailable", "Audit service is unavailable; request was not sent upstream.")
			return
		}
		userID, keyID := apiKey.User.ID, apiKey.ID
		var email *string
		if value := strings.TrimSpace(apiKey.User.Email); value != "" {
			email = &value
		}
		var requestedModel *string
		if model := gjson.GetBytes(body, "model"); model.Type == gjson.String && strings.TrimSpace(model.String()) != "" {
			value := model.String()
			requestedModel = &value
		}
		transport := "http"
		if c.Request.URL.Path != "/v1/messages/count_tokens" && gjson.GetBytes(body, "stream").Bool() {
			transport = "sse"
		}
		result, err := audit.AdmitRequest(c.Request.Context(), service.AuditAdmissionInput{
			GatewayRequestID: gatewayID, SubjectUserID: &userID, SubjectEmailSnapshot: email,
			APIKeyID: &keyID, ProfileVersion: profileVersion, Protocol: protocol,
			Endpoint: c.Request.URL.Path, Method: c.Request.Method, Transport: transport,
			RequestedModel: requestedModel, Plaintext: envelope,
		})
		if err != nil {
			writeCoreGatewayProtocolError(c, protocol, http.StatusServiceUnavailable, "gateway_audit_unavailable", "Audit service is unavailable; request was not sent upstream.")
			return
		}
		c.Set(auditInteractionIDKey, result.InteractionID)
		c.Set(auditAdmissionResultKey, result)
		c.Set(auditTransportKey, transport)
		c.Next()
		if rejection, rejected := auditPreUpstreamModelRejectionFromContext(c); rejected {
			summary := rejection.SafeSummary
			_ = audit.AdvanceRequestOutcome(c.Request.Context(), service.AuditStateCAS{
				InteractionID: result.InteractionID, ExpectedState: service.AuditRequestProcessing,
				ExpectedVersion: 0, NextState: service.AuditRequestRejectedPreUpstream,
				At: time.Now().UTC(), SafeErrorSummary: &summary,
			})
		}
	}
}

func encodeAuditRequestEnvelope(r *http.Request, body []byte) ([]byte, error) {
	names := make([]string, 0, len(auditAllowedRequestHeaders))
	for name := range auditAllowedRequestHeaders {
		if len(r.Header.Values(name)) > 0 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	headers := make([]auditCapturedHeader, 0, len(names))
	for _, name := range names {
		headers = append(headers, auditCapturedHeader{Name: name, Values: append([]string(nil), r.Header.Values(name)...)})
	}
	requestURI := r.RequestURI
	if requestURI == "" && r.URL != nil {
		requestURI = r.URL.RequestURI()
	}
	return json.Marshal(auditRequestEnvelope{Version: "core-gateway-request-v1", Method: r.Method, RequestURI: requestURI, Headers: headers, Body: append([]byte(nil), body...)})
}
