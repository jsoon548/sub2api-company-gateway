package middleware

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// CoreGatewayEntryShape is a protocol entry shape, not a client identity.
// Claude Code, Codex, and OpenCode may use a shape only when their fixture
// matches it; product names and User-Agent values never widen this allowlist.
type CoreGatewayEntryShape struct {
	Path       string
	AllowHTTP  bool
	AllowSSE   bool
	RequireAPI string
}

const (
	CoreGatewayAPIAnthropicVersion = "anthropic-version"
)

// CoreGatewayStrictEntryProfile validates the exact method, path, transport,
// protocol header, and basic JSON body shape after Audit Admission/Continuity.
// Handler-native validation remains authoritative for operation-specific JSON.
func CoreGatewayStrictEntryProfile(profileVersion string, shape CoreGatewayEntryShape) gin.HandlerFunc {
	protocol := protocolForCoreGatewayProfile(profileVersion)
	return func(c *gin.Context) {
		if c.Request == nil || c.Request.URL == nil || c.Request.Method != http.MethodPost || c.Request.URL.Path != shape.Path || isCoreGatewayWebSocket(c.Request) {
			rejectCoreGatewayEntry(c, protocol, "entry_shape_not_allowed")
			return
		}

		mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || !strings.EqualFold(mediaType, "application/json") {
			rejectCoreGatewayEntry(c, protocol, "content_type_not_allowed")
			return
		}
		if shape.RequireAPI != "" && strings.TrimSpace(c.GetHeader(shape.RequireAPI)) == "" {
			markAuditPreUpstreamRejection(c, "required_protocol_header_missing")
			writeCoreGatewayProtocolError(c, protocol, http.StatusBadRequest, "invalid_request_error", "A required protocol header is missing; request was not sent upstream.")
			return
		}

		body, readErr := io.ReadAll(c.Request.Body)
		if readErr != nil {
			markAuditPreUpstreamRejection(c, "request_body_unreadable")
			writeCoreGatewayProtocolError(c, protocol, http.StatusBadRequest, "invalid_request_error", "Failed to read request body.")
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		if len(body) == 0 || !gjson.ValidBytes(body) || !strings.HasPrefix(strings.TrimSpace(string(body)), "{") {
			markAuditPreUpstreamRejection(c, "request_body_invalid_json_object")
			writeCoreGatewayProtocolError(c, protocol, http.StatusBadRequest, "invalid_request_error", "Request body must be a JSON object.")
			return
		}

		stream := gjson.GetBytes(body, "stream")
		if stream.Exists() && stream.Type != gjson.True && stream.Type != gjson.False {
			markAuditPreUpstreamRejection(c, "stream_type_invalid")
			writeCoreGatewayProtocolError(c, protocol, http.StatusBadRequest, "invalid_request_error", "stream must be a boolean.")
			return
		}
		isSSE := stream.Bool()
		if (isSSE && !shape.AllowSSE) || (!isSSE && !shape.AllowHTTP) {
			rejectCoreGatewayEntry(c, protocol, "transport_not_allowed")
			return
		}
		c.Next()
	}
}

// CoreGatewayRequireResolvedPlatform prevents a verified operation such as
// compact from being silently emulated through a different provider protocol.
func CoreGatewayRequireResolvedPlatform(platform string) gin.HandlerFunc {
	return func(c *gin.Context) {
		resolution, ok := service.ExplicitModelResolutionFromContext(c.Request.Context())
		if !ok || resolution.Platform != platform {
			markAuditPreUpstreamRejection(c, "resolved_platform_not_supported_for_entry")
			writeCoreGatewayProtocolError(c, coreGatewayProtocolOpenAI, http.StatusForbidden, "gateway_model_not_allowed", "The explicitly selected model is not supported for this entry; request was not sent upstream.")
			return
		}
		c.Next()
	}
}

func rejectCoreGatewayEntry(c *gin.Context, protocol, summary string) {
	markAuditPreUpstreamRejection(c, summary)
	writeCoreGatewayProtocolError(c, protocol, http.StatusForbidden, "gateway_entry_not_allowed", "This method, path, transport, or profile is not allowed; request was not sent upstream.")
}

func isCoreGatewayWebSocket(r *http.Request) bool {
	if r == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") ||
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

// CoreGatewayNoRouteDefaultDeny gives unknown aliases, subpaths, and methods a
// protocol-native error and Gateway-owned ID without capturing rejected body
// content. Non-Gateway paths retain Gin's normal 404 behavior.
func CoreGatewayNoRouteDefaultDeny() gin.HandlerFunc {
	return func(c *gin.Context) {
		profile, recognized := coreGatewayRejectedPathProfile(c.Request.URL.Path)
		if !recognized {
			c.Status(http.StatusNotFound)
			return
		}
		CoreGatewayRequestID()(c)
		CoreGatewayEntryNotAllowed(profile)(c)
	}
}

func coreGatewayRejectedPathProfile(requestPath string) (string, bool) {
	clean := strings.TrimRight(strings.TrimSpace(requestPath), "/")
	if clean == "" {
		clean = "/"
	}
	for _, prefix := range []string{"/v1/messages", "/messages"} {
		if clean == prefix || strings.HasPrefix(clean, prefix+"/") {
			return service.ProtocolProfileAnthropicMessagesV1, true
		}
	}
	for _, prefix := range []string{"/v1/responses", "/responses", "/backend-api/codex/responses", "/v1/chat/completions", "/chat/completions"} {
		if clean == prefix || strings.HasPrefix(clean, prefix+"/") {
			return service.ProtocolProfileOpenAIResponsesV1, true
		}
	}
	return "", false
}
