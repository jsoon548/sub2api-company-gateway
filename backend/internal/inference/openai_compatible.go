package inference

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

var internalInferenceEnvName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

type openAICompatibleGenerator struct {
	backend          config.InternalInferenceBackendConfig
	profile          config.InternalInferenceProfileConfig
	endpoint         string
	credential       string
	client           *http.Client
	maxResponseBytes int64
}

type openAIResponseFormat struct {
	Type       string                   `json:"type"`
	JSONSchema openAIResponseJSONSchema `json:"json_schema"`
}

type openAIResponseJSONSchema struct {
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type openAICompatibleResponse struct {
	ID      string                   `json:"id"`
	Model   string                   `json:"model"`
	Choices []openAICompatibleChoice `json:"choices"`
	Usage   *openAICompatibleUsage   `json:"usage"`
}

type openAICompatibleWireRequest struct {
	apicompat.ChatCompletionsRequest
	EnableThinking *bool `json:"enable_thinking,omitempty"`
}

type openAICompatibleChoice struct {
	Message      openAICompatibleMessage `json:"message"`
	FinishReason string                  `json:"finish_reason"`
}

type openAICompatibleMessage struct {
	Content json.RawMessage `json:"content"`
	Refusal string          `json:"refusal"`
}

type openAICompatibleUsage struct {
	PromptTokens     *int64 `json:"prompt_tokens"`
	CompletionTokens *int64 `json:"completion_tokens"`
}

func newOpenAICompatibleGenerator(
	backend config.InternalInferenceBackendConfig,
	profile config.InternalInferenceProfileConfig,
	server config.ServerConfig,
) (StructuredGenerator, string) {
	if backend.Type != BackendTypeOpenAICompatibleChatCompletions {
		return nil, "unsupported_backend_type"
	}
	if backend.Name == "" || backend.Provider == "" || profile.Backend != backend.Name || profile.Model == "" {
		return nil, "profile_config_incomplete"
	}
	if profile.TimeoutMS <= 0 || profile.TimeoutMS > 2000 {
		return nil, "invalid_timeout"
	}
	if profile.MaxInputBytes <= 0 || profile.MaxInputBytes > 1024*1024 ||
		profile.MaxOutputUnits <= 0 || profile.MaxOutputUnits > 4096 ||
		profile.MaxResponseBytes <= 0 || profile.MaxResponseBytes > 1024*1024 ||
		profile.PromptVersion == "" || profile.SchemaVersion == "" {
		return nil, "invalid_profile_limits_or_version"
	}
	parsed, reason := validateBaseURL(backend.BaseURL, backend.AllowedHost, backend.AllowPrivateNetworkHTTP, server)
	if reason != "" {
		return nil, reason
	}
	credential, ok := resolveCredential(backend.CredentialRef)
	if !ok {
		return nil, "credential_unavailable"
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, "http_transport_unavailable"
	}
	clonedTransport := transport.Clone()
	if isLoopbackHost(parsed.Hostname()) || backend.AllowPrivateNetworkHTTP && isPrivateNetworkHost(parsed.Hostname()) {
		clonedTransport.Proxy = nil
	}
	endpoint := strings.TrimRight(parsed.String(), "/") + "/chat/completions"
	return &openAICompatibleGenerator{
		backend: backend, profile: profile, endpoint: endpoint, credential: credential,
		client: &http.Client{
			Transport: clonedTransport,
			Timeout:   time.Duration(profile.TimeoutMS) * time.Millisecond,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		maxResponseBytes: profile.MaxResponseBytes,
	}, ""
}

func validateBaseURL(rawURL, allowedHost string, allowPrivateNetworkHTTP bool, server config.ServerConfig) (*url.URL, string) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, "invalid_backend_url"
	}
	hostname := strings.ToLower(strings.Trim(parsed.Hostname(), "[]"))
	if hostname == "" || hostname != strings.ToLower(strings.Trim(strings.TrimSpace(allowedHost), "[]")) {
		return nil, "host_not_approved"
	}
	cleanPath := path.Clean(parsed.EscapedPath())
	if cleanPath != "." && cleanPath != "/" && cleanPath != "/v1" {
		return nil, "invalid_backend_base_path"
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	parsed.RawPath = ""
	loopback := isLoopbackHost(hostname)
	privateNetwork := isPrivateNetworkHost(hostname)
	if parsed.Scheme == "http" && !loopback && allowPrivateNetworkHTTP && !privateNetwork {
		return nil, "private_network_http_requires_private_ip"
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && (loopback || allowPrivateNetworkHTTP && privateNetwork)) {
		return nil, "https_required"
	}
	if loopback && parsed.Port() == strconv.Itoa(server.Port) {
		return nil, "recursive_gateway_url"
	}
	return parsed, ""
}

func isPrivateNetworkHost(host string) bool {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	ip := net.ParseIP(host)
	return ip != nil && ip.To4() != nil && ip.IsPrivate()
}

func isLoopbackHost(host string) bool {
	host = strings.ToLower(strings.Trim(strings.TrimSpace(host), "[]"))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func resolveCredential(ref string) (string, bool) {
	const prefix = "env:"
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, prefix) {
		return "", false
	}
	name := strings.TrimPrefix(ref, prefix)
	upperName := strings.ToUpper(name)
	if !internalInferenceEnvName.MatchString(name) || !strings.Contains(upperName, "INTERNAL_INFERENCE") {
		return "", false
	}
	for _, forbidden := range []string{"AUDIT", "WORK_SESSION", "JWT", "TOTP", "PAYMENT", "PEPPER"} {
		if strings.Contains(upperName, forbidden) {
			return "", false
		}
	}
	if upperName == "INTERNAL_INFERENCE_BACKEND_CREDENTIAL_REF" {
		return "", false
	}
	credential, ok := os.LookupEnv(name)
	credential = strings.TrimSpace(credential)
	return credential, ok && credential != ""
}

func (g *openAICompatibleGenerator) Generate(ctx context.Context, input StructuredRequest) (StructuredResult, error) {
	result := StructuredResult{
		Backend: g.backend.Name, Provider: g.backend.Provider, Model: g.profile.Model,
		PromptVersion: input.PromptVersion, SchemaVersion: input.SchemaVersion,
	}
	started := time.Now()
	finish := func() {
		result.LatencyMS = time.Since(started).Milliseconds()
		if result.LatencyMS < 0 {
			result.LatencyMS = 0
		}
	}
	if input.Profile != ProfileAutoComplexity || input.PromptVersion != g.profile.PromptVersion ||
		input.SchemaVersion != g.profile.SchemaVersion || strings.TrimSpace(input.System) == "" ||
		strings.TrimSpace(input.Input) == "" || len(input.Input) > g.profile.MaxInputBytes ||
		!validSchemaDocument(input.JSONSchema) {
		finish()
		return result, newError(ErrorInvalidRequest, 0, nil)
	}
	responseFormat, err := json.Marshal(openAIResponseFormat{
		Type:       "json_schema",
		JSONSchema: openAIResponseJSONSchema{Name: "auto_complexity", Strict: true, Schema: input.JSONSchema},
	})
	if err != nil {
		finish()
		return result, newError(ErrorInvalidRequest, 0, err)
	}
	systemContent, _ := json.Marshal(input.System)
	userContent, _ := json.Marshal(input.Input)
	maxOutput := g.profile.MaxOutputUnits
	wireRequest := openAICompatibleWireRequest{
		ChatCompletionsRequest: apicompat.ChatCompletionsRequest{
			Model: g.profile.Model,
			Messages: []apicompat.ChatMessage{
				{Role: "system", Content: systemContent},
				{Role: "user", Content: userContent},
			},
			MaxCompletionTokens: &maxOutput,
			ResponseFormat:      responseFormat,
			Stream:              false,
		},
	}
	if g.profile.DisableThinking {
		enableThinking := false
		wireRequest.EnableThinking = &enableThinking
	}
	body, err := json.Marshal(wireRequest)
	if err != nil {
		finish()
		return result, newError(ErrorInvalidRequest, 0, err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, bytes.NewReader(body))
	if err != nil {
		finish()
		return result, newError(ErrorInvalidRequest, 0, err)
	}
	request.Header.Set("Authorization", "Bearer "+g.credential)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := g.client.Do(request)
	if err != nil {
		finish()
		return result, classifyTransportError(ctx, err)
	}
	defer response.Body.Close()
	result.ProviderRequestID = strings.TrimSpace(response.Header.Get("X-Request-ID"))
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		finish()
		return result, classifyHTTPStatus(response.StatusCode)
	}
	limitedBody, err := io.ReadAll(io.LimitReader(response.Body, g.maxResponseBytes+1))
	if err != nil {
		finish()
		return result, newError(ErrorInvalidResponse, 0, err)
	}
	if int64(len(limitedBody)) > g.maxResponseBytes {
		finish()
		return result, newError(ErrorResponseTooLarge, 0, nil)
	}
	if len(bytes.TrimSpace(limitedBody)) == 0 {
		finish()
		return result, newError(ErrorEmptyResponse, 0, nil)
	}
	var wireResponse openAICompatibleResponse
	if err := json.Unmarshal(limitedBody, &wireResponse); err != nil {
		finish()
		return result, newError(ErrorInvalidResponse, 0, err)
	}
	if result.ProviderRequestID == "" {
		result.ProviderRequestID = strings.TrimSpace(wireResponse.ID)
	}
	if strings.TrimSpace(wireResponse.Model) != "" {
		result.Model = strings.TrimSpace(wireResponse.Model)
	}
	if len(wireResponse.Choices) != 1 {
		finish()
		return result, newError(ErrorEmptyResponse, 0, nil)
	}
	choice := wireResponse.Choices[0]
	if strings.TrimSpace(choice.Message.Refusal) != "" || choice.FinishReason == "content_filter" {
		finish()
		return result, newError(ErrorRefused, 0, nil)
	}
	var content string
	if err := json.Unmarshal(choice.Message.Content, &content); err != nil || strings.TrimSpace(content) == "" {
		finish()
		return result, newError(ErrorEmptyResponse, 0, err)
	}
	structured := json.RawMessage(strings.TrimSpace(content))
	if !json.Valid(structured) || choice.FinishReason == "length" {
		finish()
		return result, newError(ErrorInvalidResponse, 0, nil)
	}
	result.JSON = append(json.RawMessage(nil), structured...)
	if wireResponse.Usage == nil || wireResponse.Usage.PromptTokens == nil || wireResponse.Usage.CompletionTokens == nil ||
		*wireResponse.Usage.PromptTokens < 0 || *wireResponse.Usage.CompletionTokens < 0 {
		finish()
		return result, newError(ErrorUsageMissing, 0, nil)
	}
	inputUnits, outputUnits := *wireResponse.Usage.PromptTokens, *wireResponse.Usage.CompletionTokens
	result.InputUnits = &inputUnits
	result.OutputUnits = &outputUnits
	finish()
	return result, nil
}

func validSchemaDocument(raw json.RawMessage) bool {
	if !json.Valid(raw) {
		return false
	}
	var schema map[string]any
	return json.Unmarshal(raw, &schema) == nil && len(schema) > 0
}

func classifyTransportError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) || errors.Is(err, context.Canceled) {
		return newError(ErrorCanceled, 0, err)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return newError(ErrorTimeout, 0, err)
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return newError(ErrorTimeout, 0, err)
	}
	return newError(ErrorUnavailable, 0, err)
}

func classifyHTTPStatus(status int) error {
	switch {
	case status == http.StatusTooManyRequests:
		return newError(ErrorRateLimited, status, nil)
	case status >= http.StatusInternalServerError:
		return newError(ErrorProvider, status, nil)
	case status >= http.StatusBadRequest:
		return newError(ErrorRejected, status, nil)
	default:
		return newError(ErrorProvider, status, nil)
	}
}
