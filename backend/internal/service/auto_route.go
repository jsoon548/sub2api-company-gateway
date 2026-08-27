package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/tidwall/sjson"
)

const (
	autoComplexityHardTimeout      = 2 * time.Second
	TaskComplexitySimple           = "simple"
	TaskComplexityGeneral          = "general"
	TaskComplexityComplex          = "complex"
	DecisionCertaintyDeterministic = "deterministic"
	DecisionCertaintyDecisive      = "decisive"
	DecisionCertaintyUncertain     = "uncertain"
	AutoComplexityVersion          = "auto-complexity-v2"
	AutoRuleVersion                = AutoComplexityVersion

	ClassifierStatusNotCalled   = "not_called"
	ClassifierStatusCompleted   = "completed"
	ClassifierStatusTimeout     = "timeout"
	ClassifierStatusInvalid     = "invalid"
	ClassifierStatusUnavailable = "unavailable"

	RouteDecisionResultSelected    = "selected"
	RouteDecisionResultUnavailable = "unavailable"
	RouteDecisionResultFailed      = "failed"

	RouteDecisionHeader = "X-Gateway-Route-Decision-ID"
	ActualModelHeader   = "X-Gateway-Actual-Model"
	ModelTierHeader     = "X-Gateway-Model-Tier"
	ModelChangeHeader   = "X-Gateway-Model-Change-Reason"
)

var (
	ErrAutoNoCandidate     = errors.New("no eligible Auto candidate")
	ErrAutoRoutingSchema   = errors.New("Auto routing schema is unavailable")
	ErrAutoRoutingConflict = errors.New("Auto routing state changed concurrently")
	errClassifierInvalid   = errors.New("complexity classifier returned invalid output")
)

type ComplexityClassifierRequest struct {
	Text                 string   `json:"text"`
	ApproxContextTokens  int      `json:"approx_context_tokens"`
	RequiredCapabilities []string `json:"required_capabilities"`
}

type ComplexityClassifierResult struct {
	Complexity  string
	Certainty   string
	ReasonCode  string
	Explanation string
	Run         GatewayInferenceRunRecord
}

type ComplexityClassifier interface {
	Classify(context.Context, ComplexityClassifierRequest) (ComplexityClassifierResult, error)
}

func validClassifierResult(result ComplexityClassifierResult) bool {
	if result.Complexity != TaskComplexitySimple && result.Complexity != TaskComplexityGeneral && result.Complexity != TaskComplexityComplex {
		return false
	}
	if result.Certainty != DecisionCertaintyDecisive && result.Certainty != DecisionCertaintyUncertain {
		return false
	}
	return validAutoComplexityReasonCode(result.ReasonCode) && strings.TrimSpace(result.Explanation) != "" &&
		utf8.RuneCountInString(result.Explanation) <= 96
}

type AutoRequestSignals struct {
	Text                 string
	ApproxContextTokens  int
	RequiredCapabilities []string
	ToolExecutionStarted bool
}

type ComplexityAssessment struct {
	Complexity          string
	Certainty           string
	Explanation         string
	DecisionSource      string
	RequestedTier       string
	RuleVersion         string
	ClassifierVersion   *string
	ClassifierStatus    string
	ClassifierLatencyMS int64
	ClassifierAttempted bool
	InferenceRun        GatewayInferenceRunRecord
}

type AutoRouteCandidate struct {
	Tier              string
	Position          int
	LogicalModel      string
	ProviderModel     string
	Capabilities      []string
	ValidFrom         time.Time
	ValidUntil        *time.Time
	EmergencyDisabled bool
}

type AutoRoutingSnapshot struct {
	SessionID            uuid.UUID
	EmployeeUserID       int64
	ProfileVersion       string
	ConfigVersion        int64
	RoutingVersion       int64
	SelectedLogicalModel string
	SelectedTier         string
	SelectedComplexity   string
	RequiredCapabilities []string
	Candidates           []AutoRouteCandidate
}

type RouteCandidateEvaluation struct {
	Tier                  string   `json:"tier"`
	Position              int      `json:"position"`
	LogicalModel          string   `json:"logical_model"`
	RequiredCapabilities  []string `json:"required_capabilities"`
	CandidateCapabilities []string `json:"candidate_capabilities"`
	Status                string   `json:"status"`
	SchedulableAccounts   int      `json:"schedulable_accounts"`
}

type GatewayInferenceRunRecord struct {
	ID                uuid.UUID `json:"id"`
	Purpose           string    `json:"purpose"`
	Profile           string    `json:"profile"`
	Backend           string    `json:"backend"`
	Provider          string    `json:"provider"`
	Model             string    `json:"model"`
	PromptVersion     string    `json:"prompt_version"`
	SchemaVersion     string    `json:"schema_version"`
	Status            string    `json:"status"`
	ProviderRequestID *string   `json:"provider_request_id,omitempty"`
	InputUnits        *int64    `json:"input_units,omitempty"`
	OutputUnits       *int64    `json:"output_units,omitempty"`
	LatencyMS         int64     `json:"latency_ms"`
	CreatedAt         time.Time `json:"created_at"`
}

type RouteDecisionRecord struct {
	ID                   uuid.UUID                  `json:"id"`
	GatewayRequestID     uuid.UUID                  `json:"gateway_request_id"`
	WorkSessionID        uuid.UUID                  `json:"work_session_id"`
	EmployeeUserID       int64                      `json:"employee_user_id"`
	ProfileVersion       string                     `json:"profile_version"`
	ConfigVersion        int64                      `json:"config_version"`
	RequiredCapabilities []string                   `json:"required_capabilities"`
	TaskComplexity       string                     `json:"task_complexity"`
	Certainty            string                     `json:"certainty"`
	Explanation          string                     `json:"explanation"`
	DecisionSource       string                     `json:"decision_source"`
	RuleVersion          string                     `json:"rule_version"`
	ClassifierRunID      *uuid.UUID                 `json:"classifier_run_id,omitempty"`
	ClassifierVersion    *string                    `json:"classifier_version,omitempty"`
	ClassifierStatus     string                     `json:"classifier_status"`
	ClassifierLatencyMS  int64                      `json:"classifier_latency_ms"`
	RequestedTier        string                     `json:"requested_tier"`
	EffectiveTier        string                     `json:"effective_tier"`
	CandidatePool        []RouteCandidateEvaluation `json:"candidate_pool"`
	ActualLogicalModel   *string                    `json:"actual_logical_model,omitempty"`
	ActualProviderModel  *string                    `json:"actual_provider_model,omitempty"`
	ChangeReason         string                     `json:"change_reason"`
	TechnicalRetryCount  int16                      `json:"technical_retry_count"`
	TechnicalRetryReason *string                    `json:"technical_retry_reason,omitempty"`
	DecisionResult       string                     `json:"decision_result"`
	RoutingLatencyMS     int64                      `json:"routing_latency_ms"`
	AuditLinked          bool                       `json:"audit_linked"`
	UsageLinked          bool                       `json:"usage_linked"`
	InferenceRun         *GatewayInferenceRunRecord `json:"inference_run,omitempty"`
	CreatedAt            time.Time                  `json:"created_at"`
	UpdatedAt            time.Time                  `json:"updated_at"`
}

type AutoRoutingMetrics struct {
	DecisionCount           int64 `json:"decision_count"`
	ClassifierCallCount     int64 `json:"classifier_call_count"`
	ClassifierTimeoutCount  int64 `json:"classifier_timeout_count"`
	ClassifierFallbackCount int64 `json:"classifier_fallback_count"`
	ClassifierP95LatencyMS  int64 `json:"classifier_p95_latency_ms"`
	RoutingP95LatencyMS     int64 `json:"routing_p95_latency_ms"`
}

type RouteDecisionWrite struct {
	Record                 RouteDecisionRecord
	ExpectedRoutingVersion int64
	SelectedCapabilities   []string
	InferenceRun           *GatewayInferenceRunRecord
}

type AutoRoutingRepository interface {
	LoadAutoRoutingSnapshot(context.Context, uuid.UUID, time.Time) (AutoRoutingSnapshot, error)
	PersistRouteDecision(context.Context, RouteDecisionWrite) (bool, error)
	FinalizeRouteDecision(context.Context, uuid.UUID, int16, string, time.Time) error
	ListRouteDecisions(context.Context, int) ([]RouteDecisionRecord, AutoRoutingMetrics, error)
}

type AutoRouteInput struct {
	GatewayRequestID uuid.UUID
	Session          WorkSessionRecord
	Body             []byte
	ProfileVersion   string
	User             ExplicitModelAuthenticatedUser
	Access           ExplicitModelGroupAccessContext
	At               time.Time
	Resolver         *ExplicitModelResolver
}

type AutoRouteResult struct {
	DecisionID           uuid.UUID
	LogicalModel         string
	ProviderModel        string
	Tier                 string
	ChangeReason         string
	RewrittenBody        []byte
	ToolExecutionStarted bool
	Runtime              *AutoRouteRuntime
}

func (s *WorkSessionService) routeRepository() (AutoRoutingRepository, error) {
	if s == nil || s.repo == nil {
		return nil, ErrAutoRoutingSchema
	}
	repo, ok := s.repo.(AutoRoutingRepository)
	if !ok {
		return nil, ErrAutoRoutingSchema
	}
	return repo, nil
}

func (s *WorkSessionService) RouteAuto(ctx context.Context, in AutoRouteInput) (AutoRouteResult, error) {
	started := time.Now()
	repo, err := s.routeRepository()
	if err != nil || in.GatewayRequestID == uuid.Nil || in.Session.ID == uuid.Nil || in.Resolver == nil {
		return AutoRouteResult{}, ErrAutoRoutingSchema
	}
	at := in.At.UTC()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	signals, err := ExtractAutoRequestSignals(in.Body)
	if err != nil {
		return AutoRouteResult{}, err
	}
	assessment := s.assessComplexity(ctx, signals)

	for attempt := 0; attempt < 2; attempt++ {
		snapshot, loadErr := repo.LoadAutoRoutingSnapshot(ctx, in.Session.ID, at)
		if loadErr != nil {
			return AutoRouteResult{}, loadErr
		}
		record, selectedCapabilities, resolution := selectAutoCandidate(ctx, snapshot, assessment, signals, in, at)
		record.ID = uuid.New()
		record.GatewayRequestID = in.GatewayRequestID
		record.WorkSessionID = in.Session.ID
		record.EmployeeUserID = in.User.ID
		record.ProfileVersion = in.ProfileVersion
		record.ConfigVersion = snapshot.ConfigVersion
		record.RuleVersion = assessment.RuleVersion
		record.ClassifierVersion = assessment.ClassifierVersion
		record.ClassifierStatus = assessment.ClassifierStatus
		record.ClassifierLatencyMS = assessment.ClassifierLatencyMS
		record.RoutingLatencyMS = time.Since(started).Milliseconds()
		record.CreatedAt = at
		record.UpdatedAt = at
		if record.RoutingLatencyMS < 0 {
			record.RoutingLatencyMS = 0
		}
		write := RouteDecisionWrite{
			Record: record, ExpectedRoutingVersion: snapshot.RoutingVersion,
			SelectedCapabilities: selectedCapabilities,
		}
		if assessment.ClassifierAttempted {
			run := assessment.InferenceRun
			if run.ID == uuid.Nil {
				run.ID = uuid.New()
			}
			if run.CreatedAt.IsZero() {
				run.CreatedAt = at
			}
			record.ClassifierRunID = &run.ID
			write.Record.ClassifierRunID = &run.ID
			write.InferenceRun = &run
		}
		committed, persistErr := repo.PersistRouteDecision(ctx, write)
		if persistErr != nil {
			return AutoRouteResult{}, persistErr
		}
		if !committed {
			continue
		}
		if record.DecisionResult != RouteDecisionResultSelected || resolution == nil {
			return AutoRouteResult{}, ErrAutoNoCandidate
		}
		rewritten, rewriteErr := sjson.SetBytes(in.Body, "model", resolution.RequestedLogicalModel)
		if rewriteErr != nil {
			return AutoRouteResult{}, fmt.Errorf("rewrite Auto logical model: %w", rewriteErr)
		}
		runtime := &AutoRouteRuntime{
			DecisionID: record.ID, LogicalModel: resolution.RequestedLogicalModel,
			Tier: record.EffectiveTier, changeReason: record.ChangeReason,
			toolExecutionStarted: signals.ToolExecutionStarted,
		}
		return AutoRouteResult{
			DecisionID: record.ID, LogicalModel: resolution.RequestedLogicalModel,
			ProviderModel: resolution.ResolvedProviderModel, Tier: record.EffectiveTier,
			ChangeReason: record.ChangeReason, RewrittenBody: rewritten,
			ToolExecutionStarted: signals.ToolExecutionStarted, Runtime: runtime,
		}, nil
	}
	return AutoRouteResult{}, ErrAutoRoutingConflict
}

func selectAutoCandidate(
	ctx context.Context,
	snapshot AutoRoutingSnapshot,
	assessment ComplexityAssessment,
	signals AutoRequestSignals,
	in AutoRouteInput,
	at time.Time,
) (RouteDecisionRecord, []string, *ExplicitModelResolution) {
	required := unionCapabilities(snapshot.RequiredCapabilities, signals.RequiredCapabilities)
	effectiveTier := maxTier(assessment.RequestedTier, snapshot.SelectedTier)
	record := RouteDecisionRecord{
		RequiredCapabilities: required, TaskComplexity: assessment.Complexity,
		Certainty: assessment.Certainty, Explanation: assessment.Explanation,
		DecisionSource: assessment.DecisionSource, RequestedTier: assessment.RequestedTier,
		EffectiveTier: effectiveTier, CandidatePool: make([]RouteCandidateEvaluation, 0, len(snapshot.Candidates)),
		DecisionResult: RouteDecisionResultUnavailable, ChangeReason: "no_eligible_candidate",
	}

	type resolvedCandidate struct {
		candidate  AutoRouteCandidate
		resolution ExplicitModelResolution
	}
	var selected *resolvedCandidate
	candidates := append([]AutoRouteCandidate(nil), snapshot.Candidates...)
	sort.SliceStable(candidates, func(i, j int) bool {
		// A healthy current session model stays pinned. Administrator order applies
		// to first selection and to replacements after the pinned model is no longer
		// eligible; this prevents automatic same-tier churn between requests.
		if snapshot.SelectedLogicalModel != "" {
			if candidates[i].LogicalModel == snapshot.SelectedLogicalModel && candidates[j].LogicalModel != snapshot.SelectedLogicalModel {
				return true
			}
			if candidates[j].LogicalModel == snapshot.SelectedLogicalModel && candidates[i].LogicalModel != snapshot.SelectedLogicalModel {
				return false
			}
		}
		left, right := tierRank(candidates[i].Tier), tierRank(candidates[j].Tier)
		if left != right {
			return left < right
		}
		return candidates[i].Position < candidates[j].Position
	})

	for _, candidate := range candidates {
		evaluation := RouteCandidateEvaluation{
			Tier: candidate.Tier, Position: candidate.Position, LogicalModel: candidate.LogicalModel,
			RequiredCapabilities:  append([]string(nil), required...),
			CandidateCapabilities: append([]string(nil), candidate.Capabilities...), Status: "below_required_tier",
		}
		if tierRank(candidate.Tier) < tierRank(effectiveTier) {
			record.CandidatePool = append(record.CandidatePool, evaluation)
			continue
		}
		if candidate.EmergencyDisabled {
			evaluation.Status = "emergency_disabled"
			record.CandidatePool = append(record.CandidatePool, evaluation)
			continue
		}
		if candidate.ValidFrom.After(at) || (candidate.ValidUntil != nil && !candidate.ValidUntil.After(at)) {
			evaluation.Status = "outside_validity"
			record.CandidatePool = append(record.CandidatePool, evaluation)
			continue
		}
		if !supportsCapabilities(candidate.Capabilities, required) {
			evaluation.Status = "capability_mismatch"
			record.CandidatePool = append(record.CandidatePool, evaluation)
			continue
		}
		resolution, err := in.Resolver.Resolve(ctx, ExplicitModelResolveInput{
			AuthenticatedUser: in.User, Access: in.Access, RequestedLogicalModel: candidate.LogicalModel,
			ProtocolProfileVersion: in.ProfileVersion,
		})
		if err != nil || resolution.ResolvedProviderModel != candidate.ProviderModel {
			evaluation.Status = "explicit_resolver_unavailable"
			record.CandidatePool = append(record.CandidatePool, evaluation)
			continue
		}
		evaluation.Status = "eligible"
		evaluation.SchedulableAccounts = len(resolution.SchedulableAccountScope)
		record.CandidatePool = append(record.CandidatePool, evaluation)
		if selected == nil {
			copyResolution := resolution
			selected = &resolvedCandidate{candidate: candidate, resolution: copyResolution}
		}
	}

	if selected == nil {
		return record, required, nil
	}
	actualModel := selected.candidate.LogicalModel
	providerModel := selected.resolution.ResolvedProviderModel
	record.ActualLogicalModel = &actualModel
	record.ActualProviderModel = &providerModel
	record.EffectiveTier = selected.candidate.Tier
	record.DecisionResult = RouteDecisionResultSelected
	record.ChangeReason = routeChangeReason(snapshot, assessment, signals, selected.candidate)
	return record, required, &selected.resolution
}

func routeChangeReason(snapshot AutoRoutingSnapshot, assessment ComplexityAssessment, signals AutoRequestSignals, selected AutoRouteCandidate) string {
	if snapshot.RoutingVersion == 0 || snapshot.SelectedLogicalModel == "" {
		if tierRank(selected.Tier) > tierRank(assessment.RequestedTier) {
			return "upward_fallback"
		}
		return "initial_selection"
	}
	if selected.LogicalModel != snapshot.SelectedLogicalModel {
		for _, candidate := range snapshot.Candidates {
			if candidate.LogicalModel == snapshot.SelectedLogicalModel && candidate.EmergencyDisabled {
				return "emergency_disable_switch"
			}
		}
	}
	if tierRank(selected.Tier) > tierRank(snapshot.SelectedTier) {
		if tierRank(assessment.RequestedTier) > tierRank(snapshot.SelectedTier) {
			return "complexity_upgrade"
		}
		if len(unionCapabilities(snapshot.RequiredCapabilities, signals.RequiredCapabilities)) > len(snapshot.RequiredCapabilities) {
			return "capability_upgrade"
		}
		return "upward_fallback"
	}
	if selected.LogicalModel != snapshot.SelectedLogicalModel {
		return "candidate_unavailable_switch"
	}
	return "session_model_retained"
}

func (s *WorkSessionService) assessComplexity(ctx context.Context, signals AutoRequestSignals) ComplexityAssessment {
	if assessment, ok := ruleAssessment(signals); ok {
		return assessment
	}
	version := AutoComplexityVersion
	base := ComplexityAssessment{
		Complexity: TaskComplexityGeneral, RequestedTier: ModelTierGeneral, Certainty: DecisionCertaintyUncertain,
		Explanation:    "Ambiguous task used the conservative general tier because the classifier was unavailable.",
		DecisionSource: "fallback", RuleVersion: AutoRuleVersion, ClassifierVersion: &version,
		ClassifierStatus: ClassifierStatusUnavailable,
	}
	if s.classifier == nil {
		return base
	}
	timeout := s.classifierTimeout
	if timeout <= 0 || timeout > autoComplexityHardTimeout {
		timeout = autoComplexityHardTimeout
	}
	classifyCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	started := time.Now()
	result, err := s.classifier.Classify(classifyCtx, ComplexityClassifierRequest{
		Text:                 signals.Text,
		ApproxContextTokens:  signals.ApproxContextTokens,
		RequiredCapabilities: append([]string(nil), signals.RequiredCapabilities...),
	})
	base.ClassifierAttempted = true
	base.ClassifierLatencyMS = time.Since(started).Milliseconds()
	base.InferenceRun = result.Run
	if result.Run.LatencyMS >= 0 {
		base.ClassifierLatencyMS = result.Run.LatencyMS
	}
	if base.ClassifierLatencyMS < 0 {
		base.ClassifierLatencyMS = 0
	}
	if err != nil {
		switch {
		case errors.Is(classifyCtx.Err(), context.DeadlineExceeded), errors.Is(err, context.DeadlineExceeded), result.Run.Status == "timeout":
			base.ClassifierStatus = ClassifierStatusTimeout
			base.Explanation = "Ambiguous task used the conservative general tier because the classifier timed out."
		case errors.Is(err, errClassifierInvalid), invalidInferenceRunStatus(result.Run.Status):
			base.ClassifierStatus = ClassifierStatusInvalid
			base.Explanation = "Ambiguous task used the conservative general tier because classifier output was invalid."
		}
		return base
	}
	base.Explanation = strings.TrimSpace(result.Explanation)
	base.ClassifierStatus = ClassifierStatusCompleted
	if result.Certainty == DecisionCertaintyUncertain {
		base.Explanation = "The classifier returned an uncertain assessment; the conservative general tier was selected."
		return base
	}
	base.Certainty = DecisionCertaintyDecisive
	base.Complexity = result.Complexity
	base.RequestedTier = tierForComplexity(result.Complexity)
	base.DecisionSource = "classifier"
	return base
}

func ruleAssessment(signals AutoRequestSignals) (ComplexityAssessment, bool) {
	text := strings.ToLower(strings.TrimSpace(signals.Text))
	complexMarkers := []string{
		"cross-file", "across files", "multiple files", "multi-file", "refactor", "architecture",
		"race condition", "concurrency", "complex reasoning", "prove that", "formal proof",
		"跨文件", "多文件", "复杂推理", "架构设计", "并发竞态", "证明",
	}
	for _, marker := range complexMarkers {
		if strings.Contains(text, marker) {
			return ComplexityAssessment{
				Complexity: TaskComplexityComplex, Certainty: DecisionCertaintyDeterministic,
				Explanation:    "A deterministic high-complexity rule matched multi-file engineering or explicit complex reasoning.",
				DecisionSource: "rule", RequestedTier: ModelTierAdvanced,
				RuleVersion: AutoRuleVersion, ClassifierStatus: ClassifierStatusNotCalled,
			}, true
		}
	}
	if signals.ApproxContextTokens <= 1200 && len(signals.RequiredCapabilities) == 0 {
		simpleMarkers := []string{
			"rewrite", "rephrase", "fix the grammar", "format this", "convert this to", "convert to json", "convert to markdown",
			"改写", "润色", "修正语法", "格式化", "转换成 json", "转换为 json", "转换成 markdown", "转换为 markdown",
		}
		for _, marker := range simpleMarkers {
			if strings.Contains(text, marker) {
				return ComplexityAssessment{
					Complexity: TaskComplexitySimple, Certainty: DecisionCertaintyDeterministic,
					Explanation:    "A deterministic rule matched a bounded rewrite or format conversion.",
					DecisionSource: "rule", RequestedTier: ModelTierEconomy,
					RuleVersion: AutoRuleVersion, ClassifierStatus: ClassifierStatusNotCalled,
				}, true
			}
		}
	}
	return ComplexityAssessment{}, false
}

func ExtractAutoRequestSignals(body []byte) (AutoRequestSignals, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return AutoRequestSignals{}, fmt.Errorf("parse Auto request: %w", err)
	}
	var textParts []string
	capabilities := make(map[string]struct{})
	toolStarted := false
	var walk func(any, string)
	walk = func(node any, key string) {
		switch typed := node.(type) {
		case map[string]any:
			if rawType, ok := typed["type"].(string); ok {
				switch strings.ToLower(rawType) {
				case "image", "input_image", "image_url":
					capabilities["image_input"] = struct{}{}
				case "tool_result", "function_call_output", "computer_initialize_state", "computer_initialize_state_data":
					capabilities["tool_use"] = struct{}{}
					toolStarted = true
				}
			}
			if role, ok := typed["role"].(string); ok && strings.EqualFold(role, "tool") {
				capabilities["tool_use"] = struct{}{}
				toolStarted = true
			}
			if tools, ok := typed["tools"].([]any); ok && len(tools) > 0 {
				capabilities["tool_use"] = struct{}{}
			}
			for childKey, child := range typed {
				walk(child, strings.ToLower(childKey))
			}
		case []any:
			for _, child := range typed {
				walk(child, key)
			}
		case string:
			switch key {
			case "text", "content", "input", "prompt", "instructions":
				trimmed := strings.TrimSpace(typed)
				if trimmed != "" && len(trimmed) <= 64*1024 {
					textParts = append(textParts, trimmed)
				}
			}
		}
	}
	walk(value, "")
	text := strings.Join(textParts, "\n")
	approxTokens := (utf8.RuneCountInString(text) + 3) / 4
	if approxTokens > 16_000 {
		capabilities["long_context"] = struct{}{}
	}
	required := make([]string, 0, len(capabilities))
	for capability := range capabilities {
		required = append(required, capability)
	}
	sort.Strings(required)
	if len(text) > 16*1024 {
		text = text[:16*1024]
	}
	return AutoRequestSignals{
		Text: text, ApproxContextTokens: approxTokens,
		RequiredCapabilities: required, ToolExecutionStarted: toolStarted,
	}, nil
}

func tierForComplexity(complexity string) string {
	switch complexity {
	case TaskComplexitySimple:
		return ModelTierEconomy
	case TaskComplexityComplex:
		return ModelTierAdvanced
	default:
		return ModelTierGeneral
	}
}

func tierRank(tier string) int {
	switch tier {
	case ModelTierEconomy:
		return 1
	case ModelTierGeneral:
		return 2
	case ModelTierAdvanced:
		return 3
	default:
		return 0
	}
}

func maxTier(left, right string) string {
	if tierRank(right) > tierRank(left) {
		return right
	}
	return left
}

func normalizeCapability(capability string) string {
	switch strings.ToLower(strings.TrimSpace(capability)) {
	case "image", "images", "vision", "image_input":
		return "image_input"
	case "tool", "tools", "tool_use", "function_calling":
		return "tool_use"
	case "long_context", "long-context":
		return "long_context"
	default:
		return strings.ToLower(strings.TrimSpace(capability))
	}
}

func supportsCapabilities(candidate, required []string) bool {
	available := make(map[string]struct{}, len(candidate))
	for _, capability := range candidate {
		available[normalizeCapability(capability)] = struct{}{}
	}
	for _, capability := range required {
		if _, ok := available[normalizeCapability(capability)]; !ok {
			return false
		}
	}
	return true
}

func unionCapabilities(values ...[]string) []string {
	seen := make(map[string]struct{})
	for _, list := range values {
		for _, capability := range list {
			normalized := normalizeCapability(capability)
			if normalized != "" {
				seen[normalized] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(seen))
	for capability := range seen {
		result = append(result, capability)
	}
	sort.Strings(result)
	return result
}

type autoRouteRuntimeContextKey struct{}

type AutoRouteRuntime struct {
	DecisionID   uuid.UUID
	LogicalModel string
	Tier         string

	mu                   sync.Mutex
	technicalRetryCount  int16
	changeReason         string
	technicalRetryReason string
	toolExecutionStarted bool
}

func WithAutoRouteRuntime(ctx context.Context, runtime *AutoRouteRuntime) context.Context {
	if runtime == nil {
		return ctx
	}
	return context.WithValue(ctx, autoRouteRuntimeContextKey{}, runtime)
}

func autoRouteRuntimeFromContext(ctx context.Context) (*AutoRouteRuntime, bool) {
	if ctx == nil {
		return nil, false
	}
	runtime, ok := ctx.Value(autoRouteRuntimeContextKey{}).(*AutoRouteRuntime)
	return runtime, ok && runtime != nil && runtime.DecisionID != uuid.Nil
}

// ClaimAutoTechnicalRetry is a no-op for explicit requests. For Auto it grants
// exactly one retry only before output and before any tool execution history.
func ClaimAutoTechnicalRetry(ctx context.Context, outputStarted bool, reason string) bool {
	runtime, ok := autoRouteRuntimeFromContext(ctx)
	if !ok {
		return true
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if outputStarted || runtime.toolExecutionStarted || runtime.technicalRetryCount >= 1 {
		return false
	}
	runtime.technicalRetryCount++
	runtime.changeReason = "technical_retry"
	runtime.technicalRetryReason = strings.TrimSpace(reason)
	if runtime.technicalRetryReason == "" {
		runtime.technicalRetryReason = "upstream_retryable_failure"
	}
	return true
}

func ApplyAutoRouteResponseHeaders(header http.Header, ctx context.Context) {
	runtime, ok := autoRouteRuntimeFromContext(ctx)
	if !ok || header == nil {
		return
	}
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	header.Set(RouteDecisionHeader, runtime.DecisionID.String())
	header.Set(ActualModelHeader, runtime.LogicalModel)
	header.Set(ModelTierHeader, runtime.Tier)
	header.Set(ModelChangeHeader, runtime.changeReason)
}

func (s *WorkSessionService) FinalizeAutoRoute(ctx context.Context, runtime *AutoRouteRuntime) error {
	if runtime == nil {
		return nil
	}
	repo, err := s.routeRepository()
	if err != nil {
		return err
	}
	runtime.mu.Lock()
	count := runtime.technicalRetryCount
	reason := runtime.technicalRetryReason
	runtime.mu.Unlock()
	return repo.FinalizeRouteDecision(ctx, runtime.DecisionID, count, reason, time.Now().UTC())
}
