package inference

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

const inferenceTestCredentialEnv = "INTERNAL_INFERENCE_INTERNAL_INFERENCE_SYNTHETIC_CREDENTIAL"

func testInferenceConfig(t *testing.T, baseURL string) config.InternalInferenceConfig {
	t.Helper()
	parsed, err := url.Parse(baseURL)
	require.NoError(t, err)
	t.Setenv(inferenceTestCredentialEnv, "synthetic-credential-not-real")
	return config.InternalInferenceConfig{
		Backend: config.InternalInferenceBackendConfig{
			Name: "synthetic-backend", Type: BackendTypeOpenAICompatibleChatCompletions,
			Provider: "synthetic-provider", BaseURL: strings.TrimRight(baseURL, "/") + "/v1",
			AllowedHost: parsed.Hostname(), CredentialRef: "env:" + inferenceTestCredentialEnv,
		},
		Profiles: config.InternalInferenceProfilesConfig{AutoComplexity: config.InternalInferenceProfileConfig{
			Enabled: true, Backend: "synthetic-backend", Model: "classifier-small", TimeoutMS: 100,
			MaxInputBytes: 16 * 1024, MaxOutputUnits: 128, MaxResponseBytes: 64 * 1024,
			PromptVersion: "auto-complexity-v2", SchemaVersion: "auto-complexity-v2",
		}},
	}
}

func testStructuredRequest() StructuredRequest {
	return StructuredRequest{
		Profile: ProfileAutoComplexity, System: "classify only", Input: `{"task_text":"ambiguous"}`,
		JSONSchema:    json.RawMessage(`{"type":"object","additionalProperties":false}`),
		PromptVersion: "auto-complexity-v2", SchemaVersion: "auto-complexity-v2",
	}
}

func writeCompletion(w http.ResponseWriter, status int, content string, usage any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-ID", "provider-request-synthetic")
	w.WriteHeader(status)
	if status < 200 || status >= 300 {
		_, _ = w.Write([]byte(`{"error":{"message":"redacted synthetic failure"}}`))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "completion-synthetic", "model": "classifier-small",
		"choices": []any{map[string]any{
			"finish_reason": "stop", "message": map[string]any{"content": content},
		}},
		"usage": usage,
	})
}

func TestOpenAICompatibleStructuredOutputProtocol(t *testing.T) {
	var calls atomic.Int64
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		require.Equal(t, "Bearer synthetic-credential-not-real", r.Header.Get("Authorization"))
		var request openAICompatibleWireRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Equal(t, "classifier-small", request.Model)
		require.False(t, request.Stream)
		require.Len(t, request.Messages, 2)
		require.Equal(t, 128, *request.MaxCompletionTokens)
		require.NotNil(t, request.EnableThinking)
		require.False(t, *request.EnableThinking)
		var format struct {
			Type       string `json:"type"`
			JSONSchema struct {
				Name   string          `json:"name"`
				Strict bool            `json:"strict"`
				Schema json.RawMessage `json:"schema"`
			} `json:"json_schema"`
		}
		require.NoError(t, json.Unmarshal(request.ResponseFormat, &format))
		require.Equal(t, "json_schema", format.Type)
		require.Equal(t, "auto_complexity", format.JSONSchema.Name)
		require.True(t, format.JSONSchema.Strict)
		require.JSONEq(t, `{"type":"object","additionalProperties":false}`, string(format.JSONSchema.Schema))
		writeCompletion(w, http.StatusOK, `{"complexity":"general"}`, map[string]any{"prompt_tokens": 12, "completion_tokens": 3})
	}))
	defer stub.Close()

	cfg := testInferenceConfig(t, stub.URL)
	cfg.Profiles.AutoComplexity.DisableThinking = true
	runtime := NewRuntime(cfg, config.ServerConfig{Port: 65000})
	require.True(t, runtime.Status().Ready)
	result, err := runtime.Generate(context.Background(), testStructuredRequest())
	require.NoError(t, err)
	require.Equal(t, int64(1), calls.Load(), "the adapter must not retry")
	require.JSONEq(t, `{"complexity":"general"}`, string(result.JSON))
	require.Equal(t, "synthetic-backend", result.Backend)
	require.Equal(t, "synthetic-provider", result.Provider)
	require.Equal(t, "classifier-small", result.Model)
	require.Equal(t, "provider-request-synthetic", result.ProviderRequestID)
	require.EqualValues(t, 12, *result.InputUnits)
	require.EqualValues(t, 3, *result.OutputUnits)
	require.True(t, runtime.Status().Ready)
}

func TestOpenAICompatibleOmitsThinkingOverrideByDefault(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request openAICompatibleWireRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Nil(t, request.EnableThinking)
		writeCompletion(w, http.StatusOK, `{"complexity":"general"}`, map[string]any{
			"prompt_tokens": 12, "completion_tokens": 3,
		})
	}))
	defer stub.Close()

	runtime := NewRuntime(testInferenceConfig(t, stub.URL), config.ServerConfig{Port: 65000})
	_, err := runtime.Generate(context.Background(), testStructuredRequest())
	require.NoError(t, err)
}

func TestOpenAICompatibleResponseFailures(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		content   string
		usage     any
		mutate    func(*config.InternalInferenceConfig)
		wantKind  ErrorKind
		writeBody func(http.ResponseWriter)
	}{
		{name: "400", status: 400, wantKind: ErrorRejected},
		{name: "401", status: 401, wantKind: ErrorRejected},
		{name: "429", status: 429, wantKind: ErrorRateLimited},
		{name: "5xx", status: 503, wantKind: ErrorProvider},
		{name: "usage missing", status: 200, content: `{}`, usage: nil, wantKind: ErrorUsageMissing},
		{name: "non json content", status: 200, content: `not-json`, usage: map[string]any{"prompt_tokens": 1, "completion_tokens": 1}, wantKind: ErrorInvalidResponse},
		{name: "empty content", status: 200, content: ``, usage: map[string]any{"prompt_tokens": 1, "completion_tokens": 1}, wantKind: ErrorEmptyResponse},
		{
			name: "oversized response", status: 200, wantKind: ErrorResponseTooLarge,
			mutate: func(cfg *config.InternalInferenceConfig) { cfg.Profiles.AutoComplexity.MaxResponseBytes = 64 },
			writeBody: func(w http.ResponseWriter) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(strings.Repeat("x", 65)))
			},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if testCase.writeBody != nil {
					testCase.writeBody(w)
					return
				}
				writeCompletion(w, testCase.status, testCase.content, testCase.usage)
			}))
			defer stub.Close()
			cfg := testInferenceConfig(t, stub.URL)
			if testCase.mutate != nil {
				testCase.mutate(&cfg)
			}
			runtime := NewRuntime(cfg, config.ServerConfig{Port: 65000})
			_, err := runtime.Generate(context.Background(), testStructuredRequest())
			require.Error(t, err)
			require.Equal(t, testCase.wantKind, Kind(err))
			require.False(t, runtime.Status().Ready)
			require.Equal(t, string(testCase.wantKind), runtime.Status().ReasonCode)
		})
	}
}

func TestOpenAICompatibleRefusalTimeoutCancellationAndTLS(t *testing.T) {
	t.Run("refusal", func(t *testing.T) {
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": "refused", "model": "classifier-small",
				"choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]any{"refusal": "synthetic refusal", "content": ""}}},
				"usage":   map[string]any{"prompt_tokens": 1, "completion_tokens": 1},
			})
		}))
		defer stub.Close()
		runtime := NewRuntime(testInferenceConfig(t, stub.URL), config.ServerConfig{Port: 65000})
		_, err := runtime.Generate(context.Background(), testStructuredRequest())
		require.Equal(t, ErrorRefused, Kind(err))
	})

	t.Run("timeout", func(t *testing.T) {
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(40 * time.Millisecond)
			writeCompletion(w, http.StatusOK, `{}`, map[string]any{"prompt_tokens": 1, "completion_tokens": 1})
		}))
		defer stub.Close()
		cfg := testInferenceConfig(t, stub.URL)
		cfg.Profiles.AutoComplexity.TimeoutMS = 10
		runtime := NewRuntime(cfg, config.ServerConfig{Port: 65000})
		_, err := runtime.Generate(context.Background(), testStructuredRequest())
		require.Equal(t, ErrorTimeout, Kind(err))
	})

	t.Run("canceled", func(t *testing.T) {
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		}))
		defer stub.Close()
		cfg := testInferenceConfig(t, stub.URL)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		runtime := NewRuntime(cfg, config.ServerConfig{Port: 65000})
		_, err := runtime.Generate(ctx, testStructuredRequest())
		require.Equal(t, ErrorCanceled, Kind(err))
	})

	t.Run("tls", func(t *testing.T) {
		stub := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		defer stub.Close()
		cfg := testInferenceConfig(t, stub.URL)
		runtime := NewRuntime(cfg, config.ServerConfig{Port: 65000})
		_, err := runtime.Generate(context.Background(), testStructuredRequest())
		require.Equal(t, ErrorUnavailable, Kind(err))
	})
}

func TestInternalInferenceConfigurationDegradesWithoutAffectingConstruction(t *testing.T) {
	base := config.InternalInferenceConfig{
		Backend: config.InternalInferenceBackendConfig{
			Name: "backend", Type: BackendTypeOpenAICompatibleChatCompletions, Provider: "provider",
			BaseURL: "http://127.0.0.1:62000/v1", AllowedHost: "127.0.0.1",
			CredentialRef: "env:" + inferenceTestCredentialEnv,
		},
		Profiles: config.InternalInferenceProfilesConfig{AutoComplexity: config.InternalInferenceProfileConfig{
			Enabled: true, Backend: "backend", Model: "model", TimeoutMS: 300,
			MaxInputBytes: 1024, MaxOutputUnits: 64, MaxResponseBytes: 4096,
			PromptVersion: "auto-complexity-v2", SchemaVersion: "auto-complexity-v2",
		}},
	}
	tests := []struct {
		name   string
		mutate func(*config.InternalInferenceConfig)
		reason string
	}{
		{name: "missing credential", reason: "credential_unavailable"},
		{name: "remote http", mutate: func(cfg *config.InternalInferenceConfig) {
			cfg.Backend.BaseURL = "http://example.invalid/v1"
			cfg.Backend.AllowedHost = "example.invalid"
		}, reason: "https_required"},
		{name: "private http without explicit opt in", mutate: func(cfg *config.InternalInferenceConfig) {
			cfg.Backend.BaseURL = "http://192.168.3.124:4000/v1"
			cfg.Backend.AllowedHost = "192.168.3.124"
		}, reason: "https_required"},
		{name: "public http rejected with private opt in", mutate: func(cfg *config.InternalInferenceConfig) {
			cfg.Backend.BaseURL = "http://203.0.113.10:4000/v1"
			cfg.Backend.AllowedHost = "203.0.113.10"
			cfg.Backend.AllowPrivateNetworkHTTP = true
		}, reason: "private_network_http_requires_private_ip"},
		{name: "host mismatch", mutate: func(cfg *config.InternalInferenceConfig) {
			cfg.Backend.BaseURL = "https://example.invalid/v1"
			cfg.Backend.AllowedHost = "other.invalid"
		}, reason: "host_not_approved"},
		{name: "recursive loopback", mutate: func(cfg *config.InternalInferenceConfig) {
			cfg.Backend.BaseURL = "http://127.0.0.1:8080/v1"
		}, reason: "recursive_gateway_url"},
		{name: "invalid path", mutate: func(cfg *config.InternalInferenceConfig) {
			cfg.Backend.BaseURL = "https://example.invalid/custom"
			cfg.Backend.AllowedHost = "example.invalid"
		}, reason: "invalid_backend_base_path"},
		{name: "timeout over hard cap", mutate: func(cfg *config.InternalInferenceConfig) {
			cfg.Profiles.AutoComplexity.TimeoutMS = 2001
		}, reason: "invalid_timeout"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv(inferenceTestCredentialEnv, "")
			cfg := base
			if testCase.name != "missing credential" {
				t.Setenv(inferenceTestCredentialEnv, "synthetic")
			}
			if testCase.mutate != nil {
				testCase.mutate(&cfg)
			}
			runtime := NewRuntime(cfg, config.ServerConfig{Port: 8080})
			require.False(t, runtime.Status().Ready)
			require.Equal(t, testCase.reason, runtime.Status().ReasonCode)
		})
	}
}

func TestInternalInferenceAllowsExplicitPrivateNetworkHTTP(t *testing.T) {
	t.Setenv(inferenceTestCredentialEnv, "synthetic")
	cfg := config.InternalInferenceConfig{
		Backend: config.InternalInferenceBackendConfig{
			Name: "backend", Type: BackendTypeOpenAICompatibleChatCompletions, Provider: "provider",
			BaseURL: "http://192.168.3.124:4000/v1", AllowedHost: "192.168.3.124",
			AllowPrivateNetworkHTTP: true, CredentialRef: "env:" + inferenceTestCredentialEnv,
		},
		Profiles: config.InternalInferenceProfilesConfig{AutoComplexity: config.InternalInferenceProfileConfig{
			Enabled: true, Backend: "backend", Model: "model", TimeoutMS: 300,
			MaxInputBytes: 1024, MaxOutputUnits: 64, MaxResponseBytes: 4096,
			PromptVersion: "auto-complexity-v2", SchemaVersion: "auto-complexity-v2",
		}},
	}

	runtime := NewRuntime(cfg, config.ServerConfig{Port: 8080})
	require.True(t, runtime.Status().Ready)
	generator, ok := runtime.generator.(*openAICompatibleGenerator)
	require.True(t, ok)
	transport, ok := generator.client.Transport.(*http.Transport)
	require.True(t, ok)
	require.Nil(t, transport.Proxy, "approved private HTTP must not use environment proxies")
	require.ErrorIs(t, generator.client.CheckRedirect(&http.Request{}, nil), http.ErrUseLastResponse)
}

func TestPrivateNetworkHostRequiresLiteralPrivateIP(t *testing.T) {
	require.True(t, isPrivateNetworkHost("192.168.3.124"))
	require.True(t, isPrivateNetworkHost("10.0.0.1"))
	require.True(t, isPrivateNetworkHost("172.16.0.1"))
	require.False(t, isPrivateNetworkHost("127.0.0.1"))
	require.False(t, isPrivateNetworkHost("fc00::1"))
	require.False(t, isPrivateNetworkHost("203.0.113.10"))
	require.False(t, isPrivateNetworkHost("gateway.example.internal"))
}

func TestTransportFailureClassification(t *testing.T) {
	require.Equal(t, ErrorUnavailable, Kind(classifyTransportError(context.Background(), &net.DNSError{Err: "synthetic", Name: "example.invalid"})))
	require.Equal(t, ErrorUnavailable, Kind(classifyTransportError(context.Background(), &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("synthetic connection failure")})))
	require.Equal(t, ErrorUnavailable, Kind(classifyTransportError(context.Background(), &tls.RecordHeaderError{})))
}
