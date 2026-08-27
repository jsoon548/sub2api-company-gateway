// Package inference owns Gateway-internal structured model calls. It is kept
// separate from employee Gateway routing, audit admission, and billing paths.
package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

const (
	BackendTypeOpenAICompatibleChatCompletions = "openai_compatible_chat_completions"
	ProfileAutoComplexity                      = "auto_complexity"
)

type ErrorKind string

const (
	ErrorInvalidRequest   ErrorKind = "invalid_request"
	ErrorUnavailable      ErrorKind = "unavailable"
	ErrorTimeout          ErrorKind = "timeout"
	ErrorCanceled         ErrorKind = "canceled"
	ErrorRejected         ErrorKind = "rejected"
	ErrorRateLimited      ErrorKind = "rate_limited"
	ErrorProvider         ErrorKind = "provider_error"
	ErrorRefused          ErrorKind = "refused"
	ErrorEmptyResponse    ErrorKind = "empty_response"
	ErrorInvalidResponse  ErrorKind = "invalid_response"
	ErrorResponseTooLarge ErrorKind = "response_too_large"
	ErrorUsageMissing     ErrorKind = "usage_missing"
)

// Error is intentionally safe to return through logs and management state. It
// never contains credentials, prompts, provider response bodies, or raw URLs.
type Error struct {
	Kind       ErrorKind
	StatusCode int
	cause      error
}

func (e *Error) Error() string {
	if e == nil {
		return "internal inference failed"
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("internal inference failed: %s (HTTP %d)", e.Kind, e.StatusCode)
	}
	return fmt.Sprintf("internal inference failed: %s", e.Kind)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func newError(kind ErrorKind, statusCode int, cause error) error {
	return &Error{Kind: kind, StatusCode: statusCode, cause: cause}
}

func Kind(err error) ErrorKind {
	var inferenceErr *Error
	if errors.As(err, &inferenceErr) {
		return inferenceErr.Kind
	}
	return ErrorUnavailable
}

type StructuredRequest struct {
	Profile       string
	System        string
	Input         string
	JSONSchema    json.RawMessage
	PromptVersion string
	SchemaVersion string
}

type StructuredResult struct {
	JSON              json.RawMessage
	Backend           string
	Provider          string
	Model             string
	ProviderRequestID string
	PromptVersion     string
	SchemaVersion     string
	InputUnits        *int64
	OutputUnits       *int64
	LatencyMS         int64
}

type StructuredGenerator interface {
	Generate(context.Context, StructuredRequest) (StructuredResult, error)
}

type ProfileStatus struct {
	Profile       string `json:"profile"`
	State         string `json:"state"`
	Ready         bool   `json:"ready"`
	ReasonCode    string `json:"reason_code"`
	Backend       string `json:"backend,omitempty"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	PromptVersion string `json:"prompt_version,omitempty"`
	SchemaVersion string `json:"schema_version,omitempty"`
}

// Runtime owns the single V1 profile and tracks its last safe readiness state.
// Construction never fails application startup; invalid or missing config
// produces a degraded runtime whose Generate method fails closed.
type Runtime struct {
	mu        sync.RWMutex
	status    ProfileStatus
	generator StructuredGenerator
}

func NewRuntime(cfg config.InternalInferenceConfig, server config.ServerConfig) *Runtime {
	profile := cfg.Profiles.AutoComplexity
	status := ProfileStatus{
		Profile: ProfileAutoComplexity, State: "degraded", ReasonCode: "configuration_missing",
		Backend: cfg.Backend.Name, Provider: cfg.Backend.Provider, Model: profile.Model,
		PromptVersion: profile.PromptVersion, SchemaVersion: profile.SchemaVersion,
	}
	runtime := &Runtime{status: status}
	if !profile.Enabled {
		runtime.status.ReasonCode = "profile_disabled"
		return runtime
	}
	generator, reason := newOpenAICompatibleGenerator(cfg.Backend, profile, server)
	if reason != "" {
		runtime.status.ReasonCode = reason
		return runtime
	}
	runtime.generator = generator
	runtime.status.State = "ready"
	runtime.status.Ready = true
	runtime.status.ReasonCode = "ready"
	return runtime
}

func (r *Runtime) Generate(ctx context.Context, request StructuredRequest) (StructuredResult, error) {
	if r == nil {
		return StructuredResult{}, newError(ErrorUnavailable, 0, nil)
	}
	r.mu.RLock()
	generator := r.generator
	r.mu.RUnlock()
	if generator == nil {
		return StructuredResult{}, newError(ErrorUnavailable, 0, nil)
	}
	result, err := generator.Generate(ctx, request)
	r.recordOutcome(err)
	return result, err
}

func (r *Runtime) Status() ProfileStatus {
	if r == nil {
		return ProfileStatus{Profile: ProfileAutoComplexity, State: "degraded", ReasonCode: "service_unavailable"}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

func (r *Runtime) MarkDegraded(reason string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status.State = "degraded"
	r.status.Ready = false
	if reason == "" {
		reason = "unavailable"
	}
	r.status.ReasonCode = reason
}

func (r *Runtime) recordOutcome(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err == nil {
		r.status.State = "ready"
		r.status.Ready = true
		r.status.ReasonCode = "ready"
		return
	}
	r.status.State = "degraded"
	r.status.Ready = false
	r.status.ReasonCode = string(Kind(err))
}
