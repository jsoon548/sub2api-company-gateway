package service

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type workSessionRepoStub struct {
	checkErr       error
	generation     int64
	reliable       map[string]WorkSessionRecord
	unreliable     []WorkSessionRecord
	lastCreate     WorkSessionCreate
	links          map[uuid.UUID]uuid.UUID
	auto           AutoConfig
	versions       []WorkSessionConfigVersion
	management     WorkSessionManagementUpdate
	emergencyModel string
	emergencyValue bool
}

func (s *workSessionRepoStub) CheckFoundation(context.Context) error { return s.checkErr }
func (s *workSessionRepoStub) CurrentGeneration(context.Context) (int64, error) {
	if s.generation == 0 {
		return 1, nil
	}
	return s.generation, nil
}
func (s *workSessionRepoStub) FindOrCreateReliable(_ context.Context, in WorkSessionCreate) (WorkSessionRecord, error) {
	in.SessionKeyHMAC = append([]byte(nil), in.SessionKeyHMAC...)
	s.lastCreate = in
	if s.reliable == nil {
		s.reliable = map[string]WorkSessionRecord{}
	}
	key := in.TenantID + ":" + strconv.FormatInt(in.EmployeeUserID, 10) + ":" + in.ProfileVersion + ":" + in.SignalSource + ":" + in.HMACKeyVersion + ":" + hex.EncodeToString(in.SessionKeyHMAC)
	if record, ok := s.reliable[key]; ok {
		record.LastGatewayRequestID = in.GatewayRequestID
		record.LastActivityAt = in.At
		s.reliable[key] = record
		return record, nil
	}
	version := in.HMACKeyVersion
	record := WorkSessionRecord{
		ID: in.ID, TenantID: in.TenantID, EmployeeUserID: in.EmployeeUserID,
		ProfileVersion: in.ProfileVersion, SignalSource: in.SignalSource,
		SignalStatus: in.SignalStatus, HMACKeyVersion: &version,
		Reliability: WorkSessionReliabilityReliable, RoutingMode: in.RoutingMode,
		ConfigVersion: in.ConfigVersion, AnalysisEligible: true, Status: "active",
		FirstGatewayRequestID: in.GatewayRequestID, LastGatewayRequestID: in.GatewayRequestID,
		CreatedAt: in.At, LastActivityAt: in.At,
	}
	s.reliable[key] = record
	return record, nil
}
func (s *workSessionRepoStub) CreateUnreliable(_ context.Context, in WorkSessionCreate) (WorkSessionRecord, error) {
	s.lastCreate = in
	record := WorkSessionRecord{
		ID: in.ID, TenantID: in.TenantID, EmployeeUserID: in.EmployeeUserID,
		ProfileVersion: in.ProfileVersion, SignalSource: in.SignalSource,
		SignalStatus: in.SignalStatus, Reliability: WorkSessionReliabilityUnreliable,
		RoutingMode: in.RoutingMode, ConfigVersion: in.ConfigVersion,
		Status: "request_scoped", FirstGatewayRequestID: in.GatewayRequestID,
		LastGatewayRequestID: in.GatewayRequestID, CreatedAt: in.At, LastActivityAt: in.At,
	}
	s.unreliable = append(s.unreliable, record)
	return record, nil
}
func (s *workSessionRepoStub) GetAutoConfig(context.Context) (AutoConfig, error) { return s.auto, nil }
func (s *workSessionRepoStub) ReplaceManagementConfig(_ context.Context, in WorkSessionManagementUpdate, _ time.Time) (int64, error) {
	s.management = in
	s.generation++
	return s.generation, nil
}
func (s *workSessionRepoStub) ListManagement(context.Context, int) (AutoConfig, []ModelCatalogEntry, []AutoCandidate, []WorkSessionRecord, error) {
	return s.auto, nil, nil, nil, nil
}
func (s *workSessionRepoStub) ListConfigVersions(context.Context, int64) ([]WorkSessionConfigVersion, error) {
	return s.versions, nil
}
func (s *workSessionRepoStub) SetEmergencyDisabled(_ context.Context, model string, disabled bool, _ time.Time) error {
	s.emergencyModel, s.emergencyValue = model, disabled
	return nil
}
func (s *workSessionRepoStub) IsModelAvailableForSession(context.Context, uuid.UUID, string, time.Time) (bool, error) {
	return true, nil
}
func (s *workSessionRepoStub) LinkGatewayRequest(_ context.Context, gatewayID, sessionID uuid.UUID) error {
	if s.links == nil {
		s.links = map[uuid.UUID]uuid.UUID{}
	}
	s.links[gatewayID] = sessionID
	return nil
}

func readyWorkSessionService(t *testing.T, repo *workSessionRepoStub) *WorkSessionService {
	t.Helper()
	keyName := "WORK_SESSION_WORK_SESSION_HMAC_SYNTHETIC_KEY"
	t.Setenv(keyName, base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	svc := NewWorkSessionService(repo, config.WorkSessionConfig{
		Mode: WorkSessionModeRequired, TenantID: "tenant-synthetic",
		HMACKeyRef: "env:" + keyName, HMACKeyVersion: "workSession-v1",
	}, config.AuditConfig{ContentKeyRef: "env:WORK_SESSION_AUDIT_SYNTHETIC_KEY"}, config.InternalInferenceConfig{}, nil)
	svc.Start()
	require.True(t, svc.Status().ReliableReady)
	return svc
}

func TestWorkSessionSignalFixture(t *testing.T) {
	var fixture struct {
		ContractVersion     string `json:"contract_version"`
		UserAgentIsIdentity bool   `json:"user_agent_is_identity"`
		Cases               []struct {
			ID             string              `json:"id"`
			ProfileVersion string              `json:"profile_version"`
			Headers        map[string][]string `json:"headers"`
			ExpectedSource string              `json:"expected_source"`
			ExpectedStatus string              `json:"expected_status"`
		} `json:"cases"`
	}
	raw, err := os.ReadFile(filepath.Join("testdata", "work_session_signals_v1.json"))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &fixture))
	require.Equal(t, "core-gateway-workSession-work-session-signals-v1", fixture.ContractVersion)
	require.False(t, fixture.UserAgentIsIdentity)
	for _, testCase := range fixture.Cases {
		t.Run(testCase.ID, func(t *testing.T) {
			headers := make(http.Header)
			for name, values := range testCase.Headers {
				for _, value := range values {
					headers.Add(name, value)
				}
			}
			signal := ExtractWorkSessionSignal(testCase.ProfileVersion, headers)
			require.Equal(t, testCase.ExpectedSource, signal.Source)
			require.Equal(t, testCase.ExpectedStatus, signal.Status)
			if signal.Status != WorkSessionSignalVerified {
				require.Empty(t, signal.Value)
			}
		})
	}
}

func TestReliableWorkSessionStableAndCollisionDomains(t *testing.T) {
	repo := &workSessionRepoStub{generation: 7}
	svc := readyWorkSessionService(t, repo)
	ctx := context.Background()
	signalValue := "11111111-1111-4111-8111-111111111111"
	base := WorkSessionAssociateInput{
		EmployeeUserID: 41, ProfileVersion: ProtocolProfileAnthropicMessagesV1,
		Signal:         WorkSessionSignal{Source: WorkSessionSignalClaudeCode, Status: WorkSessionSignalVerified, Value: signalValue},
		RequestedModel: "company-model", GatewayRequestID: uuid.New(),
	}
	first, err := svc.AssociateRequest(ctx, base)
	require.NoError(t, err)
	base.GatewayRequestID = uuid.New()
	second, err := svc.AssociateRequest(ctx, base)
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, int64(7), first.ConfigVersion)
	require.True(t, first.AnalysisEligible)
	require.False(t, first.QuotaGraceEligible, "quota extension quota grace must not be granted by work-session implementation")
	require.NotEqual(t, []byte(signalValue), repo.lastCreate.SessionKeyHMAC)
	require.Len(t, repo.lastCreate.SessionKeyHMAC, sha256.Size)
	versionOneDigest := append([]byte(nil), repo.lastCreate.SessionKeyHMAC...)
	versionTwoRepo := &workSessionRepoStub{generation: 7}
	versionTwoService := NewWorkSessionService(versionTwoRepo, config.WorkSessionConfig{
		Mode: WorkSessionModeRequired, TenantID: "tenant-synthetic",
		HMACKeyRef: "env:WORK_SESSION_WORK_SESSION_HMAC_SYNTHETIC_KEY", HMACKeyVersion: "workSession-v2",
	}, config.AuditConfig{}, config.InternalInferenceConfig{}, nil)
	versionTwoService.Start()
	base.GatewayRequestID = uuid.New()
	_, err = versionTwoService.AssociateRequest(ctx, base)
	require.NoError(t, err)
	require.NotEqual(t, versionOneDigest, versionTwoRepo.lastCreate.SessionKeyHMAC, "configured key version must domain-separate the derived Work Session Key")

	variants := []WorkSessionAssociateInput{
		func() WorkSessionAssociateInput {
			v := base
			v.EmployeeUserID = 42
			v.GatewayRequestID = uuid.New()
			return v
		}(),
		func() WorkSessionAssociateInput {
			v := base
			v.ProfileVersion = ProtocolProfileOpenAIResponsesV1
			v.GatewayRequestID = uuid.New()
			return v
		}(),
		func() WorkSessionAssociateInput {
			v := base
			v.Signal.Source = WorkSessionSignalOpenCode
			v.GatewayRequestID = uuid.New()
			return v
		}(),
	}
	for _, variant := range variants {
		record, variantErr := svc.AssociateRequest(ctx, variant)
		require.NoError(t, variantErr)
		require.NotEqual(t, first.ID, record.ID)
	}
	require.Len(t, repo.reliable, 4)
}

func TestWorkSessionHMACReferenceMustBeIndependentAndExplicit(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	tests := []struct {
		name, ref, auditRef, reason string
	}{
		{name: "same as audit", ref: "env:WORK_SESSION_WORK_SESSION_HMAC_SHARED", auditRef: "env:WORK_SESSION_WORK_SESSION_HMAC_SHARED", reason: "hmac_key_not_independent"},
		{name: "audit named ref", ref: "env:WORK_SESSION_AUDIT_WORK_SESSION_HMAC", reason: "hmac_key_unavailable"},
		{name: "employee pepper named ref", ref: "env:WORK_SESSION_EMPLOYEE_PEPPER_WORK_SESSION_HMAC", reason: "hmac_key_unavailable"},
		{name: "api key named ref", ref: "env:WORK_SESSION_API_KEY_WORK_SESSION_HMAC", reason: "hmac_key_unavailable"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			name := strings.TrimPrefix(testCase.ref, "env:")
			t.Setenv(name, key)
			svc := NewWorkSessionService(&workSessionRepoStub{}, config.WorkSessionConfig{
				Mode: WorkSessionModeRequired, TenantID: "tenant-synthetic",
				HMACKeyRef: testCase.ref, HMACKeyVersion: "workSession-v1",
			}, config.AuditConfig{ContentKeyRef: testCase.auditRef}, config.InternalInferenceConfig{}, nil)
			svc.Start()
			require.False(t, svc.Status().ReliableReady)
			require.Equal(t, testCase.reason, svc.Status().ReasonCode)
		})
	}
}

func TestUnreliableWorkSessionsAreRequestScopedAndIneligible(t *testing.T) {
	repo := &workSessionRepoStub{generation: 3}
	svc := readyWorkSessionService(t, repo)
	input := WorkSessionAssociateInput{
		EmployeeUserID: 9, ProfileVersion: ProtocolProfileOpenAIResponsesV1,
		Signal:         WorkSessionSignal{Source: WorkSessionSignalOpenCode, Status: WorkSessionSignalMalformed},
		RequestedModel: "explicit-model", GatewayRequestID: uuid.New(),
	}
	first, err := svc.AssociateRequest(context.Background(), input)
	require.NoError(t, err)
	input.GatewayRequestID = uuid.New()
	second, err := svc.AssociateRequest(context.Background(), input)
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)
	for _, record := range []WorkSessionRecord{first, second} {
		require.Equal(t, WorkSessionReliabilityUnreliable, record.Reliability)
		require.Equal(t, "request_scoped", record.Status)
		require.False(t, record.AnalysisEligible)
		require.False(t, record.QuotaGraceEligible)
		require.Nil(t, record.HMACKeyVersion)
	}
}

func TestAutoBoundaryDefaultsExplicitAndRequiresReadyPilot(t *testing.T) {
	repo := &workSessionRepoStub{generation: 2, auto: AutoConfig{Enabled: true, UserWhitelist: []int64{7}, ConfigVersion: 2}}
	svc := readyWorkSessionService(t, repo)
	reliable := WorkSessionRecord{ID: uuid.New(), Reliability: WorkSessionReliabilityReliable}
	decision, err := svc.EvaluateAutoBoundary(context.Background(), "explicit-model", 8, 9, WorkSessionRecord{})
	require.NoError(t, err)
	require.False(t, decision.EnterAuto)
	require.Equal(t, "explicit_model", decision.Reason)

	_, err = svc.EvaluateAutoBoundary(context.Background(), "auto", 7, 9, WorkSessionRecord{Reliability: WorkSessionReliabilityUnreliable})
	require.ErrorIs(t, err, ErrAutoReliableRequired)
	_, err = svc.EvaluateAutoBoundary(context.Background(), "auto", 8, 9, reliable)
	require.ErrorIs(t, err, ErrAutoNotAllowed)
	decision, err = svc.EvaluateAutoBoundary(context.Background(), "auto", 7, 9, reliable)
	require.NoError(t, err)
	require.True(t, decision.EnterAuto)

	repo.auto.Enabled = false
	_, err = svc.EvaluateAutoBoundary(context.Background(), "auto", 7, 9, reliable)
	require.ErrorIs(t, err, ErrAutoDisabled)
}

func TestMissingHMACClosesReliableAndAutoButNotExplicitBoundary(t *testing.T) {
	repo := &workSessionRepoStub{generation: 1}
	svc := NewWorkSessionService(repo, config.WorkSessionConfig{
		Mode: WorkSessionModeRequired, TenantID: "tenant-synthetic",
		HMACKeyRef: "env:WORK_SESSION_WORK_SESSION_HMAC_MISSING", HMACKeyVersion: "workSession-v1",
	}, config.AuditConfig{}, config.InternalInferenceConfig{}, nil)
	svc.Start()
	require.True(t, svc.Status().SchemaReady)
	require.False(t, svc.Status().ReliableReady)

	_, err := svc.AssociateRequest(context.Background(), WorkSessionAssociateInput{
		EmployeeUserID: 1, ProfileVersion: ProtocolProfileOpenAIResponsesV1,
		Signal:         WorkSessionSignal{Source: WorkSessionSignalCodex, Status: WorkSessionSignalVerified, Value: uuid.NewString()},
		RequestedModel: "explicit-model", GatewayRequestID: uuid.New(),
	})
	require.ErrorIs(t, err, ErrWorkSessionUnavailable)
	decision, err := svc.EvaluateAutoBoundary(context.Background(), "explicit-model", 1, 1, WorkSessionRecord{})
	require.NoError(t, err)
	require.Equal(t, "explicit_model", decision.Reason)
	_, err = svc.EvaluateAutoBoundary(context.Background(), "auto", 1, 1, WorkSessionRecord{Reliability: WorkSessionReliabilityReliable})
	require.ErrorIs(t, err, ErrWorkSessionUnavailable)
}

func TestManagementValidationKeepsTiersCapabilitiesAndOrder(t *testing.T) {
	repo := &workSessionRepoStub{generation: 4}
	svc := readyWorkSessionService(t, repo)
	state, err := svc.ReplaceManagementConfig(context.Background(), WorkSessionManagementUpdate{
		AutoEnabled: true, UserWhitelist: []int64{9, 9, -1}, GroupWhitelist: []int64{3},
		Catalog: []ModelCatalogInput{
			{LogicalModel: "economy-model", ProviderModel: "provider-e", Tier: ModelTierEconomy, Capabilities: []string{"code", "code"}},
			{LogicalModel: "general-model", ProviderModel: "provider-g", Tier: ModelTierGeneral, Capabilities: []string{"tools"}},
			{LogicalModel: "advanced-model", ProviderModel: "provider-a", Tier: ModelTierAdvanced, Capabilities: []string{"long_context"}},
		},
		CandidatePools: []AutoCandidatePoolInput{
			{Tier: ModelTierEconomy, Candidates: []string{"economy-model"}},
			{Tier: ModelTierGeneral, Candidates: []string{"general-model"}},
			{Tier: ModelTierAdvanced, Candidates: []string{"advanced-model"}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, svc.Status(), state.Status)
	require.Equal(t, []int64{9}, repo.management.UserWhitelist)
	require.Equal(t, []string{"code"}, repo.management.Catalog[0].Capabilities)
	require.Equal(t, []string{"general-model"}, repo.management.CandidatePools[1].Candidates)

	_, err = svc.ReplaceManagementConfig(context.Background(), WorkSessionManagementUpdate{
		Catalog:        []ModelCatalogInput{{LogicalModel: "x", ProviderModel: "x", Tier: ModelTierEconomy}},
		CandidatePools: []AutoCandidatePoolInput{{Tier: ModelTierAdvanced, Candidates: []string{"x"}}},
	})
	require.ErrorIs(t, err, ErrWorkSessionInvalid)
}

func TestManagementStateExposesVersionContentsAndAutoHistoryLimit(t *testing.T) {
	repo := &workSessionRepoStub{
		auto: AutoConfig{Enabled: false, UserWhitelist: []int64{7}, ConfigVersion: 3},
		versions: []WorkSessionConfigVersion{
			{
				ConfigVersion: 3, Current: true, SessionCount: 1, ReliableSessionCount: 1,
				Catalog: []ModelCatalogEntry{{LogicalModel: "current-model", ProviderModel: "provider-current", Tier: ModelTierGeneral}},
			},
			{
				ConfigVersion: 2, SessionCount: 2, ReliableSessionCount: 1, RequestScopedSessionCount: 1,
				Catalog: []ModelCatalogEntry{{LogicalModel: "old-model", ProviderModel: "provider-old", Tier: ModelTierEconomy}},
			},
		},
	}
	svc := readyWorkSessionService(t, repo)
	state, err := svc.GetManagementState(context.Background())
	require.NoError(t, err)
	require.Len(t, state.ConfigVersions, 2)
	require.Equal(t, WorkSessionAutoSnapshotCurrent, state.ConfigVersions[0].AutoSnapshotStatus)
	require.NotNil(t, state.ConfigVersions[0].Auto)
	require.Equal(t, []int64{7}, state.ConfigVersions[0].Auto.UserWhitelist)
	require.Equal(t, WorkSessionAutoSnapshotNotRecorded, state.ConfigVersions[1].AutoSnapshotStatus)
	require.Nil(t, state.ConfigVersions[1].Auto)
	require.Equal(t, "provider-old", state.ConfigVersions[1].Catalog[0].ProviderModel)
}
