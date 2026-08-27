package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type autoRoutingRepoStub struct {
	*workSessionRepoStub
	snapshot AutoRoutingSnapshot
	writes   []RouteDecisionWrite
}

func (s *autoRoutingRepoStub) LoadAutoRoutingSnapshot(context.Context, uuid.UUID, time.Time) (AutoRoutingSnapshot, error) {
	copy := s.snapshot
	copy.RequiredCapabilities = append([]string(nil), s.snapshot.RequiredCapabilities...)
	copy.Candidates = append([]AutoRouteCandidate(nil), s.snapshot.Candidates...)
	return copy, nil
}

func (s *autoRoutingRepoStub) PersistRouteDecision(_ context.Context, write RouteDecisionWrite) (bool, error) {
	if write.ExpectedRoutingVersion != s.snapshot.RoutingVersion {
		return false, nil
	}
	s.writes = append(s.writes, write)
	if write.Record.DecisionResult == RouteDecisionResultSelected {
		s.snapshot.RoutingVersion++
		s.snapshot.SelectedLogicalModel = *write.Record.ActualLogicalModel
		s.snapshot.SelectedTier = write.Record.EffectiveTier
		s.snapshot.SelectedComplexity = write.Record.TaskComplexity
		s.snapshot.RequiredCapabilities = append([]string(nil), write.SelectedCapabilities...)
	}
	return true, nil
}

func (s *autoRoutingRepoStub) FinalizeRouteDecision(context.Context, uuid.UUID, int16, string, time.Time) error {
	return nil
}

func (s *autoRoutingRepoStub) ListRouteDecisions(context.Context, int) ([]RouteDecisionRecord, AutoRoutingMetrics, error) {
	return nil, AutoRoutingMetrics{}, nil
}

type autoClassifierStub struct {
	result  ComplexityClassifierResult
	timeout bool
	calls   int
}

type deadlineCapturingClassifier struct {
	remaining time.Duration
}

func (s *deadlineCapturingClassifier) Classify(ctx context.Context, _ ComplexityClassifierRequest) (ComplexityClassifierResult, error) {
	deadline, ok := ctx.Deadline()
	if ok {
		s.remaining = time.Until(deadline)
	}
	return ComplexityClassifierResult{
		Complexity: TaskComplexityGeneral, Certainty: DecisionCertaintyDecisive,
		ReasonCode: "ordinary_default", Explanation: "Ordinary task.",
	}, nil
}

func (s *autoClassifierStub) Classify(ctx context.Context, _ ComplexityClassifierRequest) (ComplexityClassifierResult, error) {
	s.calls++
	result := s.result
	if result.ReasonCode == "" {
		result.ReasonCode = "ordinary_default"
	}
	if result.Run.ID == uuid.Nil {
		inputUnits, outputUnits := int64(2), int64(1)
		result.Run = GatewayInferenceRunRecord{
			ID: uuid.New(), Purpose: "auto_complexity_classification", Profile: "auto_complexity",
			Backend: "synthetic-backend", Provider: "synthetic-provider", Model: "classifier-small",
			PromptVersion: AutoComplexityVersion, SchemaVersion: AutoComplexityVersion,
			Status: "completed", InputUnits: &inputUnits, OutputUnits: &outputUnits, LatencyMS: 1,
			CreatedAt: time.Now().UTC(),
		}
	}
	if s.timeout {
		<-ctx.Done()
		result.Run.Status = "timeout"
		return result, ctx.Err()
	}
	return result, nil
}

type autoCatalogStub struct{ unavailable map[string]bool }

func (s *autoCatalogStub) FindApprovedExplicitModel(_ context.Context, input ExplicitModelResolveInput) (*ExplicitModelApprovalSnapshot, error) {
	if s.unavailable[input.RequestedLogicalModel] {
		return nil, ErrExplicitModelUnavailable
	}
	return &ExplicitModelApprovalSnapshot{
		EntryID: input.RequestedLogicalModel, GroupID: input.Access.GroupID, ChannelID: 9,
		LogicalModel: input.RequestedLogicalModel, Platform: PlatformOpenAI,
		ResolvedProviderModel:   "provider-" + input.RequestedLogicalModel,
		SchedulableAccountScope: []int64{11, 12}, ConfigurationVersion: 1,
	}, nil
}

func autoRoutingSnapshot(sessionID uuid.UUID) AutoRoutingSnapshot {
	now := time.Now().UTC().Add(-time.Hour)
	return AutoRoutingSnapshot{
		SessionID: sessionID, EmployeeUserID: 42, ProfileVersion: ProtocolProfileOpenAIResponsesV1,
		ConfigVersion: 2,
		Candidates: []AutoRouteCandidate{
			{Tier: ModelTierEconomy, Position: 1, LogicalModel: "economy-model", ProviderModel: "provider-economy-model", ValidFrom: now},
			{Tier: ModelTierGeneral, Position: 1, LogicalModel: "general-model", ProviderModel: "provider-general-model", Capabilities: []string{"image_input", "tool_use", "long_context"}, ValidFrom: now},
			{Tier: ModelTierAdvanced, Position: 1, LogicalModel: "advanced-model", ProviderModel: "provider-advanced-model", Capabilities: []string{"image_input", "tool_use", "long_context"}, ValidFrom: now},
		},
	}
}

func routeInput(sessionID uuid.UUID, body []byte, resolver *ExplicitModelResolver) AutoRouteInput {
	return AutoRouteInput{
		GatewayRequestID: uuid.New(), Session: WorkSessionRecord{ID: sessionID}, Body: body,
		ProfileVersion: ProtocolProfileOpenAIResponsesV1,
		User:           ExplicitModelAuthenticatedUser{ID: 42, Status: StatusActive, Role: RoleUser},
		Access:         ExplicitModelGroupAccessContext{GroupID: 7}, Resolver: resolver,
	}
}

func TestAutoRoutingGolden(t *testing.T) {
	var fixture struct {
		ContractVersion string `json:"contract_version"`
		Cases           []struct {
			ID                       string                     `json:"id"`
			Body                     json.RawMessage            `json:"body"`
			Classifier               ComplexityClassifierResult `json:"classifier"`
			ClassifierTimeout        bool                       `json:"classifier_timeout"`
			UnavailableModels        []string                   `json:"unavailable_models"`
			ExpectedTier             string                     `json:"expected_tier"`
			ExpectedModel            string                     `json:"expected_model"`
			ExpectedClassifierStatus string                     `json:"expected_classifier_status"`
			ExpectedCertainty        string                     `json:"expected_certainty"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "auto_routing_golden_v1.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.Equal(t, "core-gateway-autoRouting-auto-routing-golden-v2", fixture.ContractVersion)

	for _, testCase := range fixture.Cases {
		t.Run(testCase.ID, func(t *testing.T) {
			sessionID := uuid.New()
			repo := &autoRoutingRepoStub{workSessionRepoStub: &workSessionRepoStub{}, snapshot: autoRoutingSnapshot(sessionID)}
			classifier := &autoClassifierStub{result: testCase.Classifier, timeout: testCase.ClassifierTimeout}
			svc := NewWorkSessionServiceWithClassifier(repo, config.WorkSessionConfig{}, config.AuditConfig{}, classifier, 10*time.Millisecond)
			unavailable := make(map[string]bool)
			for _, model := range testCase.UnavailableModels {
				unavailable[model] = true
			}
			resolver := NewExplicitModelResolver(&autoCatalogStub{unavailable: unavailable})
			result, err := svc.RouteAuto(context.Background(), routeInput(sessionID, testCase.Body, resolver))
			require.NoError(t, err)
			require.Equal(t, testCase.ExpectedTier, result.Tier)
			require.Equal(t, testCase.ExpectedModel, result.LogicalModel)
			require.Len(t, repo.writes, 1)
			require.Equal(t, testCase.ExpectedClassifierStatus, repo.writes[0].Record.ClassifierStatus)
			require.Equal(t, testCase.ExpectedCertainty, repo.writes[0].Record.Certainty)
			if testCase.ExpectedClassifierStatus == ClassifierStatusNotCalled {
				require.Zero(t, classifier.calls)
				require.Nil(t, repo.writes[0].InferenceRun)
			} else {
				require.Equal(t, 1, classifier.calls)
				require.NotNil(t, repo.writes[0].InferenceRun)
			}
		})
	}
}

func TestAutoComplexityUsesV2TwoSecondDeadline(t *testing.T) {
	classifier := &deadlineCapturingClassifier{}
	svc := NewWorkSessionServiceWithClassifier(
		&workSessionRepoStub{}, config.WorkSessionConfig{}, config.AuditConfig{}, classifier, 2*time.Second,
	)
	assessment := svc.assessComplexity(context.Background(), AutoRequestSignals{Text: "ambiguous task"})
	require.Equal(t, ClassifierStatusCompleted, assessment.ClassifierStatus)
	require.Greater(t, classifier.remaining, 1500*time.Millisecond)
	require.LessOrEqual(t, classifier.remaining, 2*time.Second)
}

func TestAutoRoutingSessionOnlyUpgradesAndEmergencyDisableSwitches(t *testing.T) {
	sessionID := uuid.New()
	repo := &autoRoutingRepoStub{workSessionRepoStub: &workSessionRepoStub{}, snapshot: autoRoutingSnapshot(sessionID)}
	classifier := &autoClassifierStub{result: ComplexityClassifierResult{Complexity: TaskComplexityGeneral, Certainty: DecisionCertaintyDecisive, Explanation: "General."}}
	svc := NewWorkSessionServiceWithClassifier(repo, config.WorkSessionConfig{}, config.AuditConfig{}, classifier, 50*time.Millisecond)
	resolver := NewExplicitModelResolver(&autoCatalogStub{})

	first, err := svc.RouteAuto(context.Background(), routeInput(sessionID, []byte(`{"model":"auto","input":"Rewrite this sentence."}`), resolver))
	require.NoError(t, err)
	require.Equal(t, ModelTierEconomy, first.Tier)

	advanced, err := svc.RouteAuto(context.Background(), routeInput(sessionID, []byte(`{"model":"auto","input":"Perform a cross-file refactor with complex reasoning."}`), resolver))
	require.NoError(t, err)
	require.Equal(t, ModelTierAdvanced, advanced.Tier)
	require.Equal(t, "complexity_upgrade", advanced.ChangeReason)

	retained, err := svc.RouteAuto(context.Background(), routeInput(sessionID, []byte(`{"model":"auto","input":"Rewrite this sentence."}`), resolver))
	require.NoError(t, err)
	require.Equal(t, ModelTierAdvanced, retained.Tier)
	require.Equal(t, "session_model_retained", retained.ChangeReason)

	for index := range repo.snapshot.Candidates {
		if repo.snapshot.Candidates[index].LogicalModel == "advanced-model" {
			repo.snapshot.Candidates[index].EmergencyDisabled = true
		}
	}
	repo.snapshot.Candidates = append(repo.snapshot.Candidates, AutoRouteCandidate{
		Tier: ModelTierAdvanced, Position: 2, LogicalModel: "advanced-backup", ProviderModel: "provider-advanced-backup",
		Capabilities: []string{"image_input", "tool_use", "long_context"}, ValidFrom: time.Now().Add(-time.Hour),
	})
	switched, err := svc.RouteAuto(context.Background(), routeInput(sessionID, []byte(`{"model":"auto","input":"Rewrite this sentence."}`), resolver))
	require.NoError(t, err)
	require.Equal(t, "advanced-backup", switched.LogicalModel)
	require.Equal(t, "emergency_disable_switch", switched.ChangeReason)
}

func TestAutoRoutingEmergencyDisableReasonWinsWhenSwitchAlsoRaisesTier(t *testing.T) {
	sessionID := uuid.New()
	snapshot := autoRoutingSnapshot(sessionID)
	snapshot.RoutingVersion = 1
	snapshot.SelectedLogicalModel = "general-model"
	snapshot.SelectedTier = ModelTierGeneral
	for index := range snapshot.Candidates {
		if snapshot.Candidates[index].LogicalModel == "general-model" {
			snapshot.Candidates[index].EmergencyDisabled = true
		}
	}
	repo := &autoRoutingRepoStub{workSessionRepoStub: &workSessionRepoStub{}, snapshot: snapshot}
	classifier := &autoClassifierStub{result: ComplexityClassifierResult{
		Complexity: TaskComplexityGeneral, Certainty: DecisionCertaintyDecisive, Explanation: "General.",
	}}
	svc := NewWorkSessionServiceWithClassifier(repo, config.WorkSessionConfig{}, config.AuditConfig{}, classifier, 50*time.Millisecond)

	result, err := svc.RouteAuto(context.Background(), routeInput(sessionID, []byte(`{"model":"auto","input":"Ambiguous request."}`), NewExplicitModelResolver(&autoCatalogStub{})))
	require.NoError(t, err)
	require.Equal(t, "advanced-model", result.LogicalModel)
	require.Equal(t, "emergency_disable_switch", result.ChangeReason)
}

func TestAutoRoutingTechnicalRetryGuard(t *testing.T) {
	runtime := &AutoRouteRuntime{DecisionID: uuid.New(), LogicalModel: "general-model", Tier: ModelTierGeneral, changeReason: "initial_selection"}
	ctx := WithAutoRouteRuntime(context.Background(), runtime)
	require.True(t, ClaimAutoTechnicalRetry(ctx, false, "account_switch"))
	require.False(t, ClaimAutoTechnicalRetry(ctx, false, "account_switch"))

	header := make(http.Header)
	ApplyAutoRouteResponseHeaders(header, ctx)
	require.Equal(t, "technical_retry", header.Get(ModelChangeHeader))

	firstByte := WithAutoRouteRuntime(context.Background(), &AutoRouteRuntime{DecisionID: uuid.New()})
	require.False(t, ClaimAutoTechnicalRetry(firstByte, true, "account_switch"))
	tool := WithAutoRouteRuntime(context.Background(), &AutoRouteRuntime{DecisionID: uuid.New(), toolExecutionStarted: true})
	require.False(t, ClaimAutoTechnicalRetry(tool, false, "account_switch"))
	require.True(t, ClaimAutoTechnicalRetry(context.Background(), false, "explicit_request_unchanged"))
}

func TestAutoRoutingNoEligibleCandidateNeverFallsDown(t *testing.T) {
	sessionID := uuid.New()
	repo := &autoRoutingRepoStub{workSessionRepoStub: &workSessionRepoStub{}, snapshot: autoRoutingSnapshot(sessionID)}
	for index := range repo.snapshot.Candidates {
		if repo.snapshot.Candidates[index].Tier != ModelTierEconomy {
			repo.snapshot.Candidates[index].EmergencyDisabled = true
		}
	}
	svc := NewWorkSessionServiceWithClassifier(repo, config.WorkSessionConfig{}, config.AuditConfig{}, nil, 50*time.Millisecond)
	resolver := NewExplicitModelResolver(&autoCatalogStub{})
	_, err := svc.RouteAuto(context.Background(), routeInput(sessionID, []byte(`{"model":"auto","input":"Do a cross-file refactor."}`), resolver))
	require.ErrorIs(t, err, ErrAutoNoCandidate)
	require.Len(t, repo.writes, 1)
	require.Equal(t, RouteDecisionResultUnavailable, repo.writes[0].Record.DecisionResult)
	require.Nil(t, repo.writes[0].Record.ActualLogicalModel)
}

func TestAutoRoutingClassifierIsNotEnteredBeforeAutoBoundary(t *testing.T) {
	classifier := &autoClassifierStub{result: ComplexityClassifierResult{
		Complexity: TaskComplexitySimple, Certainty: DecisionCertaintyDecisive, Explanation: "unused",
	}}
	repo := &workSessionRepoStub{
		auto: AutoConfig{Enabled: false, UserWhitelist: []int64{42}, ConfigVersion: 2},
	}
	svc := NewWorkSessionServiceWithClassifier(repo, config.WorkSessionConfig{}, config.AuditConfig{}, classifier, 50*time.Millisecond)
	session := WorkSessionRecord{ID: uuid.New(), Reliability: WorkSessionReliabilityReliable}

	decision, err := svc.EvaluateAutoBoundary(context.Background(), "explicit-model", 42, 7, session)
	require.NoError(t, err)
	require.Equal(t, "explicit_model", decision.Reason)
	require.Zero(t, classifier.calls)

	// Capability unavailable, Auto disabled, and outside-pilot checks all happen
	// before RouteAuto, which is the only method that can invoke the classifier.
	_, err = svc.EvaluateAutoBoundary(context.Background(), WorkSessionRoutingAuto, 42, 7, session)
	require.ErrorIs(t, err, ErrWorkSessionUnavailable)
	require.Zero(t, classifier.calls)

	svc.status = WorkSessionStatus{AutoCapabilityReady: true}
	_, err = svc.EvaluateAutoBoundary(context.Background(), WorkSessionRoutingAuto, 42, 7, session)
	require.ErrorIs(t, err, ErrAutoDisabled)
	require.Zero(t, classifier.calls)

	repo.auto.Enabled = true
	repo.auto.UserWhitelist = nil
	_, err = svc.EvaluateAutoBoundary(context.Background(), WorkSessionRoutingAuto, 42, 7, session)
	require.ErrorIs(t, err, ErrAutoNotAllowed)
	require.Zero(t, classifier.calls)
}

func TestAutoRoutingInvalidClassifierOutputFallsBackToGeneral(t *testing.T) {
	sessionID := uuid.New()
	repo := &autoRoutingRepoStub{workSessionRepoStub: &workSessionRepoStub{}, snapshot: autoRoutingSnapshot(sessionID)}
	classifier := &autoClassifierStub{result: ComplexityClassifierResult{Complexity: "economical", Certainty: "maybe"}}
	svc := NewWorkSessionServiceWithClassifier(repo, config.WorkSessionConfig{}, config.AuditConfig{}, classifier, 50*time.Millisecond)
	// Interface stubs are trusted to return the same contract as HTTP clients, so
	// this test injects the validation error explicitly.
	classifierWithError := ComplexityClassifierFunc(func(context.Context, ComplexityClassifierRequest) (ComplexityClassifierResult, error) {
		return ComplexityClassifierResult{Run: GatewayInferenceRunRecord{
			ID: uuid.New(), Purpose: "auto_complexity_classification", Profile: "auto_complexity",
			Backend: "synthetic-backend", Provider: "synthetic-provider", Model: "classifier-small",
			PromptVersion: AutoComplexityVersion, SchemaVersion: AutoComplexityVersion,
			Status: "invalid_response", CreatedAt: time.Now().UTC(),
		}}, errClassifierInvalid
	})
	svc.classifier = classifierWithError
	result, err := svc.RouteAuto(context.Background(), routeInput(sessionID, []byte(`{"model":"auto","input":"Ambiguous request."}`), NewExplicitModelResolver(&autoCatalogStub{})))
	require.NoError(t, err)
	require.Equal(t, ModelTierGeneral, result.Tier)
	require.Equal(t, ClassifierStatusInvalid, repo.writes[0].Record.ClassifierStatus)
}

func TestAutoRoutingUnavailableClassifierFallsBackToGeneralWithoutInferenceRun(t *testing.T) {
	sessionID := uuid.New()
	repo := &autoRoutingRepoStub{workSessionRepoStub: &workSessionRepoStub{}, snapshot: autoRoutingSnapshot(sessionID)}
	svc := NewWorkSessionServiceWithClassifier(repo, config.WorkSessionConfig{}, config.AuditConfig{}, nil, 50*time.Millisecond)

	result, err := svc.RouteAuto(context.Background(), routeInput(sessionID, []byte(`{"model":"auto","input":"Ambiguous request."}`), NewExplicitModelResolver(&autoCatalogStub{})))
	require.NoError(t, err)
	require.Equal(t, ModelTierGeneral, result.Tier)
	require.Equal(t, ClassifierStatusUnavailable, repo.writes[0].Record.ClassifierStatus)
	require.Nil(t, repo.writes[0].InferenceRun)
}

type ComplexityClassifierFunc func(context.Context, ComplexityClassifierRequest) (ComplexityClassifierResult, error)

func (f ComplexityClassifierFunc) Classify(ctx context.Context, input ComplexityClassifierRequest) (ComplexityClassifierResult, error) {
	if f == nil {
		return ComplexityClassifierResult{}, errors.New("nil classifier")
	}
	return f(ctx, input)
}
