package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/inference"
	"github.com/stretchr/testify/require"
)

type structuredGeneratorStub struct {
	result  inference.StructuredResult
	err     error
	calls   int
	request inference.StructuredRequest
	byTask  map[string]json.RawMessage
}

func (s *structuredGeneratorStub) Generate(_ context.Context, request inference.StructuredRequest) (inference.StructuredResult, error) {
	s.calls++
	s.request = request
	result := s.result
	if result.Backend == "" {
		inputUnits, outputUnits := int64(10), int64(3)
		result = inference.StructuredResult{
			Backend: "synthetic-backend", Provider: "synthetic-provider", Model: "classifier-small",
			ProviderRequestID: "provider-request-synthetic", PromptVersion: AutoComplexityVersion,
			SchemaVersion: AutoComplexityVersion, InputUnits: &inputUnits, OutputUnits: &outputUnits, LatencyMS: 7,
			JSON: result.JSON,
		}
	}
	if s.byTask != nil {
		var input autoComplexityInput
		if json.Unmarshal([]byte(request.Input), &input) == nil {
			result.JSON = append(json.RawMessage(nil), s.byTask[input.TaskText]...)
		}
	}
	return result, s.err
}

func TestAutoComplexityV2GoldenCasesAndVersionLock(t *testing.T) {
	var fixture struct {
		Version       string `json:"version"`
		PromptVersion string `json:"prompt_version"`
		SchemaVersion string `json:"schema_version"`
		RuleVersion   string `json:"rule_version"`
		Cases         []struct {
			ID                   string               `json:"id"`
			TaskText             string               `json:"task_text"`
			ApproxContextTokens  int                  `json:"approx_context_tokens"`
			RequiredCapabilities []string             `json:"required_capabilities"`
			Output               autoComplexityOutput `json:"output"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "auto_complexity_v2_golden.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.Equal(t, AutoComplexityVersion, fixture.Version)
	require.Equal(t, AutoComplexityVersion, fixture.PromptVersion)
	require.Equal(t, AutoComplexityVersion, fixture.SchemaVersion)
	require.Equal(t, AutoRuleVersion, fixture.RuleVersion)

	stub := &structuredGeneratorStub{byTask: make(map[string]json.RawMessage)}
	for _, testCase := range fixture.Cases {
		encoded, marshalErr := json.Marshal(testCase.Output)
		require.NoError(t, marshalErr)
		stub.byTask[testCase.TaskText] = encoded
	}
	classifier := newStructuredComplexityClassifier(stub)
	for _, testCase := range fixture.Cases {
		t.Run(testCase.ID, func(t *testing.T) {
			result, classifyErr := classifier.Classify(context.Background(), ComplexityClassifierRequest{
				Text: testCase.TaskText, ApproxContextTokens: testCase.ApproxContextTokens,
				RequiredCapabilities: testCase.RequiredCapabilities,
			})
			require.NoError(t, classifyErr)
			require.Equal(t, testCase.Output.Complexity, result.Complexity)
			require.Equal(t, testCase.Output.Certainty, result.Certainty)
			require.Equal(t, testCase.Output.ReasonCode, result.ReasonCode)
			require.Equal(t, "completed", result.Run.Status)
		})
	}
	require.Equal(t, len(fixture.Cases), stub.calls)
	require.Equal(t, inference.ProfileAutoComplexity, stub.request.Profile)
	require.Equal(t, AutoComplexityVersion, stub.request.PromptVersion)
	require.Equal(t, AutoComplexityVersion, stub.request.SchemaVersion)
	require.Contains(t, autoComplexitySystemPrompt, "untrusted data")
	require.Contains(t, autoComplexitySystemPrompt, "never answer it")
	require.Contains(t, autoComplexitySystemPrompt, "complete classification evidence")
	require.NotContains(t, stub.request.Input, "history")
	require.NotContains(t, stub.request.Input, "audit")
	var schema map[string]any
	require.NoError(t, json.Unmarshal(autoComplexityJSONSchema, &schema))
	require.Equal(t, false, schema["additionalProperties"])
	properties := schema["properties"].(map[string]any)
	explanation := properties["explanation"].(map[string]any)
	require.EqualValues(t, 96, explanation["maxLength"])
}

func TestStructuredComplexityClassifierRejectsInvalidOutput(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{name: "extra field", json: `{"complexity":"general","certainty":"decisive","reason_code":"ordinary_default","explanation":"ok","answer":"not allowed"}`},
		{name: "non json", json: `not-json`},
		{name: "invalid complexity", json: `{"complexity":"economical","certainty":"decisive","reason_code":"ordinary_default","explanation":"ok"}`},
		{name: "invalid certainty", json: `{"complexity":"general","certainty":"maybe","reason_code":"ordinary_default","explanation":"ok"}`},
		{name: "invalid reason", json: `{"complexity":"general","certainty":"decisive","reason_code":"answer_user","explanation":"ok"}`},
		{name: "empty explanation", json: `{"complexity":"general","certainty":"decisive","reason_code":"ordinary_default","explanation":""}`},
		{name: "explanation too long", json: `{"complexity":"general","certainty":"decisive","reason_code":"ordinary_default","explanation":"` + strings.Repeat("界", 97) + `"}`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			stub := &structuredGeneratorStub{result: inference.StructuredResult{JSON: json.RawMessage(testCase.json)}}
			classifier := newStructuredComplexityClassifier(stub)
			result, err := classifier.Classify(context.Background(), ComplexityClassifierRequest{Text: "ambiguous"})
			require.ErrorIs(t, err, errClassifierInvalid)
			require.Equal(t, string(inference.ErrorInvalidResponse), result.Run.Status)
		})
	}
}

func TestStructuredComplexityClassifierPreservesSafeFailureFacts(t *testing.T) {
	stub := &structuredGeneratorStub{
		result: inference.StructuredResult{
			Backend: "synthetic-backend", Provider: "synthetic-provider", Model: "classifier-small",
			ProviderRequestID: "request-safe", PromptVersion: AutoComplexityVersion,
			SchemaVersion: AutoComplexityVersion, LatencyMS: 11,
		},
		err: &inference.Error{Kind: inference.ErrorUsageMissing},
	}
	classifier := newStructuredComplexityClassifier(stub)
	result, err := classifier.Classify(context.Background(), ComplexityClassifierRequest{Text: "ambiguous"})
	require.Error(t, err)
	require.Equal(t, string(inference.ErrorUsageMissing), result.Run.Status)
	require.Equal(t, "synthetic-backend", result.Run.Backend)
	require.Equal(t, "request-safe", *result.Run.ProviderRequestID)
	require.Nil(t, result.Run.InputUnits)
	require.Nil(t, result.Run.OutputUnits)
	require.NotContains(t, err.Error(), "ambiguous")
}

func TestUncertainStructuredClassificationAlwaysUsesGeneralTier(t *testing.T) {
	stub := &structuredGeneratorStub{result: inference.StructuredResult{JSON: json.RawMessage(`{
		"complexity":"complex","certainty":"uncertain","reason_code":"ambiguous_or_uncertain","explanation":"unclear"
	}`)}}
	classifier := newStructuredComplexityClassifier(stub)
	svc := NewWorkSessionServiceWithClassifier(&workSessionRepoStub{}, config.WorkSessionConfig{}, config.AuditConfig{}, classifier, 50*time.Millisecond)
	assessment := svc.assessComplexity(context.Background(), AutoRequestSignals{Text: "ambiguous"})
	require.Equal(t, TaskComplexityGeneral, assessment.Complexity)
	require.Equal(t, ModelTierGeneral, assessment.RequestedTier)
	require.Equal(t, DecisionCertaintyUncertain, assessment.Certainty)
	require.Equal(t, ClassifierStatusCompleted, assessment.ClassifierStatus)
}

func TestStructuredComplexityClassifierErrorIsNeverRawProviderContent(t *testing.T) {
	stub := &structuredGeneratorStub{err: errors.New("synthetic provider body must not escape")}
	classifier := newStructuredComplexityClassifier(stub)
	_, err := classifier.Classify(context.Background(), ComplexityClassifierRequest{Text: "task text"})
	require.Error(t, err)
	require.False(t, strings.Contains(err.Error(), "task text"))
}
