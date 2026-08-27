package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/inference"
	"github.com/google/uuid"
)

const (
	WorkSessionModeDisabled = "disabled"
	WorkSessionModeRequired = "required"

	WorkSessionSignalVerified  = "verified"
	WorkSessionSignalMissing   = "missing"
	WorkSessionSignalMalformed = "malformed"

	WorkSessionSignalClaudeCode = "claude_code_session_header_v1"
	WorkSessionSignalCodex      = "codex_session_header_v1"
	WorkSessionSignalOpenCode   = "opencode_client_request_header_v1"
	WorkSessionSignalNone       = "none"

	WorkSessionReliabilityReliable   = "reliable"
	WorkSessionReliabilityUnreliable = "unreliable"

	WorkSessionRoutingExplicit = "explicit"
	WorkSessionRoutingAuto     = "auto"

	ModelTierEconomy  = "economy"
	ModelTierGeneral  = "general"
	ModelTierAdvanced = "advanced"

	WorkSessionAutoSettingKey = "core_gateway_auto_config_v1"
)

var (
	ErrWorkSessionUnavailable = errors.New("reliable Work Session capability is unavailable")
	ErrWorkSessionSchema      = errors.New("Work Session schema is unavailable")
	ErrWorkSessionInvalid     = errors.New("invalid Work Session or Auto configuration")
	ErrAutoDisabled           = errors.New("Auto is disabled")
	ErrAutoNotAllowed         = errors.New("employee is not in the Auto pilot")
	ErrAutoReliableRequired   = errors.New("Auto requires a reliable Work Session")
	ErrModelCatalogNotFound   = errors.New("model catalog entry not found")
)

var (
	workSessionSecretEnvName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	opencodeSessionID        = regexp.MustCompile(`^ses_[A-Za-z0-9_-]{8,120}$`)
	capabilityName           = regexp.MustCompile(`^[a-z][a-z0-9_:-]{0,63}$`)
)

// WorkSessionSignal is a client-supplied identifier observation. Value is
// intentionally excluded from JSON and must never cross the Service/Repository
// boundary; only its HMAC is persisted.
type WorkSessionSignal struct {
	Source string `json:"signal_source"`
	Status string `json:"signal_status"`
	Value  string `json:"-"`
}

type WorkSessionRecord struct {
	ID                    uuid.UUID `json:"id"`
	TenantID              string    `json:"tenant_id"`
	EmployeeUserID        int64     `json:"employee_user_id"`
	ProfileVersion        string    `json:"profile_version"`
	SignalSource          string    `json:"signal_source"`
	SignalStatus          string    `json:"signal_status"`
	HMACKeyVersion        *string   `json:"hmac_key_version,omitempty"`
	Reliability           string    `json:"reliability"`
	RoutingMode           string    `json:"routing_mode"`
	ConfigVersion         int64     `json:"config_version"`
	AnalysisEligible      bool      `json:"analysis_eligible"`
	QuotaGraceEligible    bool      `json:"quota_grace_eligible"`
	Status                string    `json:"status"`
	SelectedLogicalModel  *string   `json:"selected_logical_model,omitempty"`
	SelectedTier          *string   `json:"selected_tier,omitempty"`
	SelectedComplexity    *string   `json:"selected_complexity,omitempty"`
	RequiredCapabilities  []string  `json:"required_capabilities"`
	RoutingVersion        int64     `json:"routing_version"`
	FirstGatewayRequestID uuid.UUID `json:"first_gateway_request_id"`
	LastGatewayRequestID  uuid.UUID `json:"last_gateway_request_id"`
	CreatedAt             time.Time `json:"created_at"`
	LastActivityAt        time.Time `json:"last_activity_at"`
}

type WorkSessionCreate struct {
	ID               uuid.UUID
	TenantID         string
	EmployeeUserID   int64
	ProfileVersion   string
	SignalSource     string
	SignalStatus     string
	SessionKeyHMAC   []byte
	HMACKeyVersion   string
	Reliability      string
	RoutingMode      string
	ConfigVersion    int64
	GatewayRequestID uuid.UUID
	At               time.Time
}

type ModelCatalogEntry struct {
	ID                uuid.UUID  `json:"id"`
	Generation        int64      `json:"generation"`
	LogicalModel      string     `json:"logical_model"`
	ProviderModel     string     `json:"provider_model"`
	Tier              string     `json:"tier"`
	Capabilities      []string   `json:"capabilities"`
	ValidFrom         time.Time  `json:"valid_from"`
	ValidUntil        *time.Time `json:"valid_until,omitempty"`
	EmergencyDisabled bool       `json:"emergency_disabled"`
}

type AutoCandidate struct {
	ID             uuid.UUID  `json:"id"`
	Generation     int64      `json:"generation"`
	Tier           string     `json:"tier"`
	Position       int        `json:"position"`
	CatalogEntryID uuid.UUID  `json:"catalog_entry_id"`
	LogicalModel   string     `json:"logical_model"`
	ValidFrom      time.Time  `json:"valid_from"`
	ValidUntil     *time.Time `json:"valid_until,omitempty"`
}

type AutoConfig struct {
	Enabled        bool    `json:"enabled"`
	UserWhitelist  []int64 `json:"user_whitelist"`
	GroupWhitelist []int64 `json:"group_whitelist"`
	ConfigVersion  int64   `json:"config_version"`
}

type ModelCatalogInput struct {
	LogicalModel  string     `json:"logical_model"`
	ProviderModel string     `json:"provider_model"`
	Tier          string     `json:"tier"`
	Capabilities  []string   `json:"capabilities"`
	ValidFrom     *time.Time `json:"valid_from,omitempty"`
	ValidUntil    *time.Time `json:"valid_until,omitempty"`
}

type AutoCandidatePoolInput struct {
	Tier       string     `json:"tier"`
	Candidates []string   `json:"candidates"`
	ValidFrom  *time.Time `json:"valid_from,omitempty"`
	ValidUntil *time.Time `json:"valid_until,omitempty"`
}

type WorkSessionManagementUpdate struct {
	AutoEnabled    bool                     `json:"auto_enabled"`
	UserWhitelist  []int64                  `json:"user_whitelist"`
	GroupWhitelist []int64                  `json:"group_whitelist"`
	Catalog        []ModelCatalogInput      `json:"catalog"`
	CandidatePools []AutoCandidatePoolInput `json:"candidate_pools"`
}

const (
	WorkSessionAutoSnapshotCurrent     = "current"
	WorkSessionAutoSnapshotNotRecorded = "not_recorded"
)

type WorkSessionConfigVersion struct {
	ConfigVersion             int64               `json:"config_version"`
	Current                   bool                `json:"current"`
	CreatedAt                 *time.Time          `json:"created_at,omitempty"`
	SessionCount              int64               `json:"session_count"`
	ReliableSessionCount      int64               `json:"reliable_session_count"`
	RequestScopedSessionCount int64               `json:"request_scoped_session_count"`
	ModelCount                int64               `json:"model_count"`
	CandidateCount            int64               `json:"candidate_count"`
	AutoSnapshotStatus        string              `json:"auto_snapshot_status"`
	Auto                      *AutoConfig         `json:"auto,omitempty"`
	Catalog                   []ModelCatalogEntry `json:"catalog"`
	CandidatePools            []AutoCandidate     `json:"candidate_pools"`
}

type WorkSessionManagementState struct {
	Status         WorkSessionStatus          `json:"status"`
	Auto           AutoConfig                 `json:"auto"`
	Catalog        []ModelCatalogEntry        `json:"catalog"`
	CandidatePools []AutoCandidate            `json:"candidate_pools"`
	ConfigVersions []WorkSessionConfigVersion `json:"config_versions"`
	RecentSessions []WorkSessionRecord        `json:"recent_sessions"`
	RouteDecisions []RouteDecisionRecord      `json:"route_decisions"`
	RoutingMetrics AutoRoutingMetrics         `json:"routing_metrics"`
}

type WorkSessionRepository interface {
	CheckFoundation(context.Context) error
	CurrentGeneration(context.Context) (int64, error)
	FindOrCreateReliable(context.Context, WorkSessionCreate) (WorkSessionRecord, error)
	CreateUnreliable(context.Context, WorkSessionCreate) (WorkSessionRecord, error)
	GetAutoConfig(context.Context) (AutoConfig, error)
	ReplaceManagementConfig(context.Context, WorkSessionManagementUpdate, time.Time) (int64, error)
	ListManagement(context.Context, int) (AutoConfig, []ModelCatalogEntry, []AutoCandidate, []WorkSessionRecord, error)
	ListConfigVersions(context.Context, int64) ([]WorkSessionConfigVersion, error)
	SetEmergencyDisabled(context.Context, string, bool, time.Time) error
	IsModelAvailableForSession(context.Context, uuid.UUID, string, time.Time) (bool, error)
	LinkGatewayRequest(context.Context, uuid.UUID, uuid.UUID) error
}

type WorkSessionStatus struct {
	Mode                string                  `json:"mode"`
	SchemaReady         bool                    `json:"schema_ready"`
	ReliableReady       bool                    `json:"reliable_ready"`
	AutoCapabilityReady bool                    `json:"auto_capability_ready"`
	ReasonCode          string                  `json:"reason_code"`
	TenantID            string                  `json:"tenant_id,omitempty"`
	HMACKeyVersion      string                  `json:"hmac_key_version,omitempty"`
	AutoComplexity      inference.ProfileStatus `json:"auto_complexity"`
}

type WorkSessionService struct {
	repo              WorkSessionRepository
	cfg               config.WorkSessionConfig
	auditCfg          config.AuditConfig
	mu                sync.RWMutex
	key               []byte
	status            WorkSessionStatus
	startOnce         sync.Once
	classifier        ComplexityClassifier
	classifierTimeout time.Duration
	inferenceRuntime  *inference.Runtime
}

func NewWorkSessionService(
	repo WorkSessionRepository,
	cfg config.WorkSessionConfig,
	auditCfg config.AuditConfig,
	internalCfg config.InternalInferenceConfig,
	runtime *inference.Runtime,
) *WorkSessionService {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	timeout := time.Duration(internalCfg.Profiles.AutoComplexity.TimeoutMS) * time.Millisecond
	service := &WorkSessionService{
		repo: repo, cfg: cfg, auditCfg: auditCfg,
		status:            WorkSessionStatus{Mode: mode, ReasonCode: "not_started"},
		classifierTimeout: timeout, inferenceRuntime: runtime,
	}
	profile := internalCfg.Profiles.AutoComplexity
	if profile.PromptVersion != AutoComplexityVersion || profile.SchemaVersion != AutoComplexityVersion {
		if runtime != nil {
			runtime.MarkDegraded("unsupported_profile_version")
		}
		return service
	}
	if runtime != nil && runtime.Status().Ready {
		service.classifier = newStructuredComplexityClassifier(runtime)
	}
	return service
}

// NewWorkSessionServiceWithClassifier keeps deterministic test substitution
// explicit without adding a process-global hook.
func NewWorkSessionServiceWithClassifier(
	repo WorkSessionRepository,
	cfg config.WorkSessionConfig,
	auditCfg config.AuditConfig,
	classifier ComplexityClassifier,
	timeout time.Duration,
) *WorkSessionService {
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	return &WorkSessionService{
		repo: repo, cfg: cfg, auditCfg: auditCfg, classifier: classifier,
		classifierTimeout: timeout, status: WorkSessionStatus{Mode: mode, ReasonCode: "not_started"},
	}
}

// Start performs one immutable deployment preflight. Keys are never generated
// or rotated by the application.
func (s *WorkSessionService) Start() {
	if s == nil {
		return
	}
	s.startOnce.Do(func() {
		mode := strings.ToLower(strings.TrimSpace(s.cfg.Mode))
		if mode == WorkSessionModeDisabled {
			s.setStatus(WorkSessionStatus{Mode: mode, ReasonCode: "disabled"})
			return
		}
		if mode != WorkSessionModeRequired || s.repo == nil {
			s.setStatus(WorkSessionStatus{Mode: mode, ReasonCode: "invalid_mode_or_repository"})
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.repo.CheckFoundation(ctx); err != nil {
			s.setStatus(WorkSessionStatus{Mode: mode, ReasonCode: "schema_not_ready"})
			return
		}
		base := WorkSessionStatus{Mode: mode, SchemaReady: true, TenantID: strings.TrimSpace(s.cfg.TenantID), HMACKeyVersion: strings.TrimSpace(s.cfg.HMACKeyVersion)}
		if base.TenantID == "" || base.HMACKeyVersion == "" {
			base.ReasonCode = "identity_config_incomplete"
			s.setStatus(base)
			return
		}
		if strings.TrimSpace(s.cfg.HMACKeyRef) == strings.TrimSpace(s.auditCfg.ContentKeyRef) && strings.TrimSpace(s.cfg.HMACKeyRef) != "" {
			base.ReasonCode = "hmac_key_not_independent"
			s.setStatus(base)
			return
		}
		key, err := resolveWorkSessionHMACKey(s.cfg.HMACKeyRef)
		if err != nil {
			base.ReasonCode = "hmac_key_unavailable"
			s.setStatus(base)
			return
		}
		s.mu.Lock()
		s.key = key
		s.mu.Unlock()
		base.ReliableReady = true
		base.AutoCapabilityReady = true
		base.ReasonCode = "ready"
		s.setStatus(base)
	})
}

func (s *WorkSessionService) setStatus(status WorkSessionStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
}

func (s *WorkSessionService) Status() WorkSessionStatus {
	if s == nil {
		return WorkSessionStatus{ReasonCode: "service_unavailable"}
	}
	s.mu.RLock()
	status := s.status
	s.mu.RUnlock()
	status.AutoComplexity = s.inferenceRuntime.Status()
	return status
}

// ExtractWorkSessionSignal recognizes only verified client-owned headers. It
// never uses User-Agent, prompt content, similarity, or scheduling state.
func ExtractWorkSessionSignal(profileVersion string, headers http.Header) WorkSessionSignal {
	switch profileVersion {
	case ProtocolProfileAnthropicMessagesV1:
		return extractUUIDSignal(headers, "X-Claude-Code-Session-Id", WorkSessionSignalClaudeCode)
	case ProtocolProfileOpenAIResponsesV1:
		codex := extractUUIDSignal(headers, "Session-Id", WorkSessionSignalCodex)
		if codex.Status != WorkSessionSignalMissing {
			return codex
		}
		value, status := singleSessionHeader(headers, "X-Client-Request-Id")
		if status != WorkSessionSignalVerified {
			return WorkSessionSignal{Source: WorkSessionSignalOpenCode, Status: status}
		}
		if _, err := uuid.Parse(value); err != nil && !opencodeSessionID.MatchString(value) {
			return WorkSessionSignal{Source: WorkSessionSignalOpenCode, Status: WorkSessionSignalMalformed}
		}
		return WorkSessionSignal{Source: WorkSessionSignalOpenCode, Status: WorkSessionSignalVerified, Value: value}
	default:
		return WorkSessionSignal{Source: WorkSessionSignalNone, Status: WorkSessionSignalMissing}
	}
}

func extractUUIDSignal(headers http.Header, name, source string) WorkSessionSignal {
	value, status := singleSessionHeader(headers, name)
	if status != WorkSessionSignalVerified {
		return WorkSessionSignal{Source: source, Status: status}
	}
	parsed, err := uuid.Parse(value)
	if err != nil || parsed == uuid.Nil {
		return WorkSessionSignal{Source: source, Status: WorkSessionSignalMalformed}
	}
	return WorkSessionSignal{Source: source, Status: WorkSessionSignalVerified, Value: parsed.String()}
}

func singleSessionHeader(headers http.Header, name string) (string, string) {
	values := headers.Values(name)
	if len(values) == 0 {
		return "", WorkSessionSignalMissing
	}
	if len(values) != 1 {
		return "", WorkSessionSignalMalformed
	}
	value := strings.TrimSpace(values[0])
	if value == "" || len(value) > 128 || strings.Contains(value, ",") || strings.ContainsAny(value, "\r\n\x00") {
		return "", WorkSessionSignalMalformed
	}
	return value, WorkSessionSignalVerified
}

type WorkSessionAssociateInput struct {
	EmployeeUserID   int64
	ProfileVersion   string
	Signal           WorkSessionSignal
	RequestedModel   string
	GatewayRequestID uuid.UUID
	At               time.Time
}

func (s *WorkSessionService) AssociateRequest(ctx context.Context, in WorkSessionAssociateInput) (WorkSessionRecord, error) {
	status := s.Status()
	if !status.SchemaReady || s.repo == nil || in.EmployeeUserID <= 0 || in.GatewayRequestID == uuid.Nil {
		return WorkSessionRecord{}, ErrWorkSessionSchema
	}
	at := in.At.UTC()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	generation, err := s.repo.CurrentGeneration(ctx)
	if err != nil {
		return WorkSessionRecord{}, fmt.Errorf("load Work Session config generation: %w", err)
	}
	if generation <= 0 {
		generation = 1
	}
	routingMode := WorkSessionRoutingExplicit
	if strings.EqualFold(strings.TrimSpace(in.RequestedModel), WorkSessionRoutingAuto) {
		routingMode = WorkSessionRoutingAuto
	}
	base := WorkSessionCreate{
		ID: uuid.New(), TenantID: status.TenantID, EmployeeUserID: in.EmployeeUserID,
		ProfileVersion: in.ProfileVersion, SignalSource: in.Signal.Source,
		SignalStatus: in.Signal.Status, RoutingMode: routingMode, ConfigVersion: generation,
		GatewayRequestID: in.GatewayRequestID, At: at,
	}
	if in.Signal.Status != WorkSessionSignalVerified {
		base.Reliability = WorkSessionReliabilityUnreliable
		record, createErr := s.repo.CreateUnreliable(ctx, base)
		if createErr != nil {
			return WorkSessionRecord{}, fmt.Errorf("create request-scoped Work Session: %w", createErr)
		}
		return record, s.repo.LinkGatewayRequest(ctx, in.GatewayRequestID, record.ID)
	}
	if !status.ReliableReady {
		return WorkSessionRecord{}, ErrWorkSessionUnavailable
	}
	digest, err := s.deriveSessionKey(in.EmployeeUserID, in.ProfileVersion, in.Signal.Source, in.Signal.Value)
	if err != nil {
		return WorkSessionRecord{}, err
	}
	base.Reliability = WorkSessionReliabilityReliable
	base.SessionKeyHMAC = digest
	base.HMACKeyVersion = status.HMACKeyVersion
	record, err := s.repo.FindOrCreateReliable(ctx, base)
	for i := range digest {
		digest[i] = 0
	}
	if err != nil {
		return WorkSessionRecord{}, fmt.Errorf("find or create reliable Work Session: %w", err)
	}
	if err := s.repo.LinkGatewayRequest(ctx, in.GatewayRequestID, record.ID); err != nil {
		return WorkSessionRecord{}, fmt.Errorf("link Gateway request to Work Session: %w", err)
	}
	return record, nil
}

func (s *WorkSessionService) deriveSessionKey(employeeID int64, profile, source, value string) ([]byte, error) {
	s.mu.RLock()
	key := append([]byte(nil), s.key...)
	tenant := s.status.TenantID
	keyVersion := s.status.HMACKeyVersion
	s.mu.RUnlock()
	if len(key) != 32 || tenant == "" || keyVersion == "" || employeeID <= 0 || strings.TrimSpace(profile) == "" || strings.TrimSpace(source) == "" || value == "" {
		return nil, ErrWorkSessionUnavailable
	}
	mac := hmac.New(sha256.New, key)
	for i := range key {
		key[i] = 0
	}
	_, _ = mac.Write([]byte("sub2api-work-session-key-v1"))
	fields := []string{keyVersion, tenant, fmt.Sprintf("%d", employeeID), profile, source, value}
	var size [4]byte
	for _, field := range fields {
		binary.BigEndian.PutUint32(size[:], uint32(len(field)))
		_, _ = mac.Write(size[:])
		_, _ = mac.Write([]byte(field))
	}
	return mac.Sum(nil), nil
}

type AutoAdmissionDecision struct {
	EnterAuto bool   `json:"enter_auto"`
	Reason    string `json:"reason"`
}

// EvaluateAutoBoundary deliberately performs no classification or routing.
// work-session implementation only decides whether model=auto may enter that future boundary.
func (s *WorkSessionService) EvaluateAutoBoundary(ctx context.Context, requestedModel string, userID, groupID int64, session WorkSessionRecord) (AutoAdmissionDecision, error) {
	if !strings.EqualFold(strings.TrimSpace(requestedModel), WorkSessionRoutingAuto) {
		return AutoAdmissionDecision{Reason: "explicit_model"}, nil
	}
	if !s.Status().AutoCapabilityReady {
		return AutoAdmissionDecision{Reason: "capability_unavailable"}, ErrWorkSessionUnavailable
	}
	if session.Reliability != WorkSessionReliabilityReliable {
		return AutoAdmissionDecision{Reason: "reliable_session_required"}, ErrAutoReliableRequired
	}
	cfg, err := s.repo.GetAutoConfig(ctx)
	if err != nil {
		return AutoAdmissionDecision{Reason: "configuration_unavailable"}, err
	}
	if !cfg.Enabled {
		return AutoAdmissionDecision{Reason: "disabled"}, ErrAutoDisabled
	}
	if !containsInt64(cfg.UserWhitelist, userID) && !containsInt64(cfg.GroupWhitelist, groupID) {
		return AutoAdmissionDecision{Reason: "outside_pilot"}, ErrAutoNotAllowed
	}
	return AutoAdmissionDecision{EnterAuto: true, Reason: "pilot_admitted"}, nil
}

func (s *WorkSessionService) GetManagementState(ctx context.Context) (WorkSessionManagementState, error) {
	if s == nil || s.repo == nil || !s.Status().SchemaReady {
		return WorkSessionManagementState{Status: s.Status()}, ErrWorkSessionSchema
	}
	auto, catalog, pools, sessions, err := s.repo.ListManagement(ctx, 50)
	if err != nil {
		return WorkSessionManagementState{Status: s.Status()}, err
	}
	versions, err := s.repo.ListConfigVersions(ctx, auto.ConfigVersion)
	if err != nil {
		return WorkSessionManagementState{Status: s.Status()}, err
	}
	for index := range versions {
		if versions[index].Current {
			currentAuto := auto
			versions[index].Auto = &currentAuto
			versions[index].AutoSnapshotStatus = WorkSessionAutoSnapshotCurrent
			continue
		}
		versions[index].AutoSnapshotStatus = WorkSessionAutoSnapshotNotRecorded
	}
	routeDecisions := make([]RouteDecisionRecord, 0)
	routingMetrics := AutoRoutingMetrics{}
	if routeRepo, ok := s.repo.(AutoRoutingRepository); ok {
		routeDecisions, routingMetrics, err = routeRepo.ListRouteDecisions(ctx, 100)
		if err != nil {
			return WorkSessionManagementState{Status: s.Status()}, err
		}
	}
	return WorkSessionManagementState{
		Status: s.Status(), Auto: auto, Catalog: catalog, CandidatePools: pools,
		ConfigVersions: versions, RecentSessions: sessions,
		RouteDecisions: routeDecisions, RoutingMetrics: routingMetrics,
	}, nil
}

func (s *WorkSessionService) ReplaceManagementConfig(ctx context.Context, input WorkSessionManagementUpdate) (WorkSessionManagementState, error) {
	if s == nil || s.repo == nil || !s.Status().SchemaReady {
		return WorkSessionManagementState{}, ErrWorkSessionSchema
	}
	normalized, err := validateManagementUpdate(input, time.Now().UTC())
	if err != nil {
		return WorkSessionManagementState{}, err
	}
	if _, err := s.repo.ReplaceManagementConfig(ctx, normalized, time.Now().UTC()); err != nil {
		return WorkSessionManagementState{}, err
	}
	return s.GetManagementState(ctx)
}

func validateManagementUpdate(input WorkSessionManagementUpdate, now time.Time) (WorkSessionManagementUpdate, error) {
	input.UserWhitelist = uniquePositiveIDs(input.UserWhitelist)
	input.GroupWhitelist = uniquePositiveIDs(input.GroupWhitelist)
	models := make(map[string]ModelCatalogInput, len(input.Catalog))
	for i := range input.Catalog {
		entry := &input.Catalog[i]
		entry.LogicalModel = strings.TrimSpace(entry.LogicalModel)
		entry.ProviderModel = strings.TrimSpace(entry.ProviderModel)
		entry.Tier = strings.ToLower(strings.TrimSpace(entry.Tier))
		if entry.LogicalModel == "" || len(entry.LogicalModel) > 100 || entry.ProviderModel == "" || len(entry.ProviderModel) > 100 || !validModelTier(entry.Tier) {
			return input, ErrWorkSessionInvalid
		}
		if _, exists := models[entry.LogicalModel]; exists {
			return input, ErrWorkSessionInvalid
		}
		entry.Capabilities = uniqueCapabilities(entry.Capabilities)
		for _, capability := range entry.Capabilities {
			if !capabilityName.MatchString(capability) {
				return input, ErrWorkSessionInvalid
			}
		}
		if entry.ValidFrom == nil {
			value := now
			entry.ValidFrom = &value
		}
		if entry.ValidUntil != nil && !entry.ValidUntil.After(entry.ValidFrom.UTC()) {
			return input, ErrWorkSessionInvalid
		}
		models[entry.LogicalModel] = *entry
	}
	seenTiers := map[string]bool{}
	for i := range input.CandidatePools {
		pool := &input.CandidatePools[i]
		pool.Tier = strings.ToLower(strings.TrimSpace(pool.Tier))
		if !validModelTier(pool.Tier) || seenTiers[pool.Tier] {
			return input, ErrWorkSessionInvalid
		}
		seenTiers[pool.Tier] = true
		if pool.ValidFrom == nil {
			value := now
			pool.ValidFrom = &value
		}
		if pool.ValidUntil != nil && !pool.ValidUntil.After(pool.ValidFrom.UTC()) {
			return input, ErrWorkSessionInvalid
		}
		seenModels := map[string]bool{}
		for j, model := range pool.Candidates {
			model = strings.TrimSpace(model)
			entry, ok := models[model]
			if !ok || entry.Tier != pool.Tier || seenModels[model] {
				return input, ErrWorkSessionInvalid
			}
			seenModels[model] = true
			pool.Candidates[j] = model
		}
	}
	return input, nil
}

func uniquePositiveIDs(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	result := make([]int64, 0, len(values))
	for _, value := range values {
		if value <= 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func uniqueCapabilities(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func validModelTier(tier string) bool {
	return tier == ModelTierEconomy || tier == ModelTierGeneral || tier == ModelTierAdvanced
}

func (s *WorkSessionService) SetEmergencyDisabled(ctx context.Context, logicalModel string, disabled bool) error {
	if s == nil || s.repo == nil || !s.Status().SchemaReady {
		return ErrWorkSessionSchema
	}
	logicalModel = strings.TrimSpace(logicalModel)
	if logicalModel == "" || len(logicalModel) > 100 {
		return ErrWorkSessionInvalid
	}
	return s.repo.SetEmergencyDisabled(ctx, logicalModel, disabled, time.Now().UTC())
}

func (s *WorkSessionService) IsModelAvailableForSession(ctx context.Context, sessionID uuid.UUID, logicalModel string, at time.Time) (bool, error) {
	if s == nil || s.repo == nil || !s.Status().SchemaReady || sessionID == uuid.Nil {
		return false, ErrWorkSessionSchema
	}
	return s.repo.IsModelAvailableForSession(ctx, sessionID, strings.TrimSpace(logicalModel), at.UTC())
}

func resolveWorkSessionHMACKey(ref string) ([]byte, error) {
	const prefix = "env:"
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, prefix) {
		return nil, ErrWorkSessionUnavailable
	}
	name := strings.TrimPrefix(ref, prefix)
	upperName := strings.ToUpper(name)
	if !workSessionSecretEnvName.MatchString(name) || !strings.Contains(upperName, "WORK_SESSION") || !strings.Contains(upperName, "HMAC") {
		return nil, ErrWorkSessionUnavailable
	}
	for _, forbidden := range []string{"AUDIT", "JWT", "TOTP", "PAYMENT", "PROVIDER", "PEPPER", "API_KEY"} {
		if strings.Contains(upperName, forbidden) {
			return nil, ErrWorkSessionUnavailable
		}
	}
	if upperName == "WORK_SESSION_HMAC_KEY_REF" {
		return nil, ErrWorkSessionUnavailable
	}
	encoded, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(encoded) == "" {
		return nil, ErrWorkSessionUnavailable
	}
	key, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(key) != 32 {
		return nil, ErrWorkSessionUnavailable
	}
	return key, nil
}

func EncodeAutoConfig(config AutoConfig) (string, error) {
	value, err := json.Marshal(config)
	return string(value), err
}

func DecodeAutoConfig(raw string) AutoConfig {
	config := AutoConfig{ConfigVersion: 1}
	if strings.TrimSpace(raw) == "" || json.Unmarshal([]byte(raw), &config) != nil {
		return AutoConfig{ConfigVersion: 1}
	}
	config.UserWhitelist = uniquePositiveIDs(config.UserWhitelist)
	config.GroupWhitelist = uniquePositiveIDs(config.GroupWhitelist)
	if config.ConfigVersion <= 0 {
		config.ConfigVersion = 1
	}
	return config
}
