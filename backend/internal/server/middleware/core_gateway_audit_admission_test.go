package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type auditAdmissionRepoStub struct {
	admitErr    error
	admitCalls  int
	interaction service.AuditInteractionRecord
	part        service.AuditContentPartRecord
	casCalls    int
	cas         service.AuditStateCAS
	onAdmit     func()
}

type auditAdmissionCatalogStub struct {
	calls int
	entry *service.ExplicitModelApprovalSnapshot
}

func (s *auditAdmissionCatalogStub) FindApprovedExplicitModel(context.Context, service.ExplicitModelResolveInput) (*service.ExplicitModelApprovalSnapshot, error) {
	s.calls++
	if s.entry == nil {
		return nil, nil
	}
	entry := *s.entry
	entry.SchedulableAccountScope = append([]int64(nil), s.entry.SchedulableAccountScope...)
	return &entry, nil
}

func (s *auditAdmissionRepoStub) CheckFoundation(context.Context) error { return nil }
func (s *auditAdmissionRepoStub) CreateInteraction(context.Context, service.AuditInteractionRecord) error {
	return nil
}
func (s *auditAdmissionRepoStub) AppendEncryptedPart(context.Context, service.AuditContentPartRecord) error {
	return nil
}
func (s *auditAdmissionRepoStub) AdmitRequest(_ context.Context, interaction service.AuditInteractionRecord, part service.AuditContentPartRecord) error {
	s.admitCalls++
	s.interaction, s.part = interaction, part
	if s.onAdmit != nil {
		s.onAdmit()
	}
	return s.admitErr
}
func (s *auditAdmissionRepoStub) CommitResponsePart(context.Context, service.AuditResponsePartCommit) error {
	return nil
}
func (s *auditAdmissionRepoStub) SetResponseWriteResult(context.Context, service.AuditResponseWriteResult) error {
	return nil
}
func (s *auditAdmissionRepoStub) FinalizeInteraction(context.Context, service.AuditInteractionFinalization) error {
	return nil
}

func TestAuditAdmissionBodyLimitFailsClosedBeforeTransaction(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &auditAdmissionRepoStub{}
	svc := newReadyAuditAdmissionService(t, repo)
	router := gin.New()
	upstream := 0
	router.POST("/v1/messages", RequestBodyLimit(16), CoreGatewayRequestID(), func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 9, User: &service.User{ID: 44, Status: service.StatusActive}})
		c.Next()
	}, CoreGatewayAuditAdmission(svc, service.ProtocolProfileAnthropicMessagesV1), func(c *gin.Context) { upstream++ })
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(bytes.Repeat([]byte("x"), 17)))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Zero(t, upstream)
	require.Zero(t, repo.admitCalls)
	require.Contains(t, w.Body.String(), "gateway_audit_unavailable")
}
func (s *auditAdmissionRepoStub) CompareAndSwapRequestOutcome(_ context.Context, change service.AuditStateCAS) (bool, error) {
	s.casCalls++
	s.cas = change
	return true, nil
}
func (s *auditAdmissionRepoStub) CompareAndSwapContentState(context.Context, service.AuditStateCAS) (bool, error) {
	return true, nil
}
func (s *auditAdmissionRepoStub) ReconcileStale(context.Context, time.Time) (int64, error) {
	return 0, nil
}

func newReadyAuditAdmissionService(t *testing.T, repo *auditAdmissionRepoStub) *service.AuditFoundationService {
	t.Helper()
	const name = "AUDIT_ADMISSION_MIDDLEWARE_AUDIT_KEY"
	t.Setenv(name, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x71}, 32)))
	svc := service.NewAuditFoundationService(repo, config.AuditConfig{Mode: service.AuditModeRequired, ContentKeyRef: "env:" + name, ContentKeyVersion: "v1", ReconcileIntervalSeconds: 3600})
	svc.Start()
	t.Cleanup(svc.Stop)
	return svc
}

func setAuditAdmissionAPIKey(c *gin.Context) {
	groupID := int64(7)
	c.Set(string(ContextKeyAPIKey), &service.APIKey{
		ID: 7, GroupID: &groupID,
		User: &service.User{ID: 42, Email: "synthetic@example.test", Status: service.StatusActive, Role: service.RoleUser},
	})
	c.Next()
}

func TestCoreGatewayRequestIDIsUniqueAndClientValueCannotOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/id", CoreGatewayRequestID(), func(c *gin.Context) {
		id, ok := GatewayRequestIDFromContext(c)
		require.True(t, ok)
		require.Equal(t, id, c.Request.Context().Value(ctxkey.GatewayRequestID))
		c.String(http.StatusOK, id.String())
	})

	seen := make(map[string]struct{})
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodGet, "/id", nil)
		req.Header.Set(GatewayRequestIDHeader, "11111111-2222-4333-8444-555555555555")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		generated := w.Header().Get(GatewayRequestIDHeader)
		_, err := uuid.Parse(generated)
		require.NoError(t, err)
		require.NotEqual(t, "11111111-2222-4333-8444-555555555555", generated)
		require.Equal(t, generated, w.Body.String())
		_, duplicate := seen[generated]
		require.False(t, duplicate)
		seen[generated] = struct{}{}
	}
}

func TestAuditAdmissionReplacesClientIDAndCapturesExactSafeEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &auditAdmissionRepoStub{}
	svc := newReadyAuditAdmissionService(t, repo)
	router := gin.New()
	upstream := 0
	router.POST("/v1/messages", CoreGatewayRequestID(), func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 7, User: &service.User{ID: 42, Email: "synthetic@example.test", Status: service.StatusActive}})
		c.Next()
	}, CoreGatewayAuditAdmission(svc, service.ProtocolProfileAnthropicMessagesV1), func(c *gin.Context) {
		upstream++
		c.Status(http.StatusNoContent)
	})
	body := []byte(`{"model":"synthetic-model","messages":[{"role":"user","content":[{"type":"text","text":"tool synthetic"},{"type":"image","source":{"type":"url","url":"https://example.test/image.png?x=1"}}]}]}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/messages?query=exact%2Bbytes", bytes.NewReader(body))
	req.Header.Set(GatewayRequestIDHeader, "11111111-2222-4333-8444-555555555555")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Authorization", "Bearer must-not-be-captured")
	req.Header.Set("Cookie", "must-not-be-captured")
	req.Header.Set("X-Api-Key", "must-not-be-captured")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Equal(t, 1, upstream)
	generated := w.Header().Get(GatewayRequestIDHeader)
	require.NotEqual(t, "11111111-2222-4333-8444-555555555555", generated)
	require.Equal(t, repo.interaction.GatewayRequestID.String(), generated)
	codec, ok := svc.Codec()
	require.True(t, ok)
	plaintext, err := codec.Decrypt(service.AuditPartAAD{InteractionID: repo.interaction.ID, GatewayRequestID: repo.interaction.GatewayRequestID, Direction: "request", Sequence: 0, AdmittedAt: repo.interaction.AdmittedAt, KeyVersion: "v1"}, repo.part.Encrypted)
	require.NoError(t, err)
	var envelope auditRequestEnvelope
	require.NoError(t, json.Unmarshal(plaintext, &envelope))
	require.Equal(t, "/v1/messages?query=exact%2Bbytes", envelope.RequestURI)
	require.Equal(t, body, envelope.Body)
	require.NotContains(t, string(plaintext), "must-not-be-captured")
	require.Contains(t, string(envelope.Body), "https://example.test/image.png?x=1")
	require.Equal(t, 1, repo.interaction.RequestPartCount)
	require.Equal(t, repo.part.Encrypted.PlaintextSHA256, repo.interaction.RequestSHA256)
	expectedPlaintext := `{"version":"core-gateway-request-v1","method":"POST","request_uri":"/v1/messages?query=exact%2Bbytes","headers":[{"name":"Anthropic-Version","values":["2023-06-01"]},{"name":"Content-Type","values":["application/json"]}],"body":"` + base64.StdEncoding.EncodeToString(body) + `"}`
	require.Equal(t, expectedPlaintext, string(plaintext), "request URI, query, tool and multimodal URL bytes are a frozen golden")
	digest := sha256.Sum256(plaintext)
	require.Equal(t, "dd182836483c7514483dac8ec5cf09a33910956b661d4d8f3a609ebe58210548", hex.EncodeToString(digest[:]))
}

func TestAuditAdmissionDoesNotFetchExternalURLs(t *testing.T) {
	var fetches atomic.Int64
	external := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { fetches.Add(1) }))
	defer external.Close()

	repo := &auditAdmissionRepoStub{}
	svc := newReadyAuditAdmissionService(t, repo)
	router := gin.New()
	router.POST("/v1/responses", CoreGatewayRequestID(), setAuditAdmissionAPIKey,
		CoreGatewayAuditAdmission(svc, service.ProtocolProfileOpenAIResponsesV1),
		func(c *gin.Context) { c.Status(http.StatusNoContent) })
	body := `{"model":"synthetic-model","input":[{"type":"input_image","image_url":"` + external.URL + `/never-fetch"}]}`
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(body)))
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Zero(t, fetches.Load())
}

func TestAuditAdmissionCommitsBeforeResolverAndUpstream(t *testing.T) {
	resolverCatalog := &auditAdmissionCatalogStub{entry: &service.ExplicitModelApprovalSnapshot{
		EntryID: "synthetic", GroupID: 7, ChannelID: 8, LogicalModel: "synthetic-model",
		Platform: service.PlatformOpenAI, ResolvedProviderModel: "synthetic-provider-model",
		SchedulableAccountScope: []int64{9}, ConfigurationVersion: 1,
	}}
	resolver := service.NewExplicitModelResolver(resolverCatalog)
	upstream := 0
	repo := &auditAdmissionRepoStub{}
	repo.onAdmit = func() {
		require.Zero(t, resolverCatalog.calls, "resolver must run only after the admission transaction returns")
		require.Zero(t, upstream, "upstream count must remain zero before admission commit")
	}
	svc := newReadyAuditAdmissionService(t, repo)
	router := gin.New()
	router.POST("/v1/responses", CoreGatewayRequestID(), setAuditAdmissionAPIKey,
		CoreGatewayAuditAdmission(svc, service.ProtocolProfileOpenAIResponsesV1),
		CoreGatewayExplicitModelAdmission(resolver, service.ProtocolProfileOpenAIResponsesV1),
		func(c *gin.Context) { upstream++; c.Status(http.StatusNoContent) })
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"synthetic-model","input":"hello"}`)))
	require.Equal(t, http.StatusNoContent, w.Code)
	require.Equal(t, 1, repo.admitCalls)
	require.Equal(t, 1, resolverCatalog.calls)
	require.Equal(t, 1, upstream)
}

func TestAuditAdmissionModelRejectionUsesSameIDAndCAS(t *testing.T) {
	repo := &auditAdmissionRepoStub{}
	svc := newReadyAuditAdmissionService(t, repo)
	router := gin.New()
	router.POST("/v1/messages", CoreGatewayRequestID(), setAuditAdmissionAPIKey,
		CoreGatewayAuditAdmission(svc, service.ProtocolProfileAnthropicMessagesV1),
		CoreGatewayExplicitModelAdmission(service.NewExplicitModelResolver(&auditAdmissionCatalogStub{}), service.ProtocolProfileAnthropicMessagesV1),
		func(c *gin.Context) { t.Fatal("model rejection reached upstream") })
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"not-approved"}`)))
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Equal(t, repo.interaction.GatewayRequestID.String(), w.Header().Get(GatewayRequestIDHeader))
	require.Equal(t, repo.interaction.GatewayRequestID.String(), gjsonString(w.Body.Bytes(), "gateway_request_id"))
	require.Equal(t, 1, repo.casCalls)
	require.Equal(t, repo.interaction.ID, repo.cas.InteractionID)
	require.Equal(t, service.AuditRequestProcessing, repo.cas.ExpectedState)
	require.Equal(t, service.AuditRequestRejectedPreUpstream, repo.cas.NextState)
	require.NotNil(t, repo.cas.SafeErrorSummary)
	require.Equal(t, "explicit_model_not_allowed", *repo.cas.SafeErrorSummary)
}

func TestAuditAdmissionDoesNotMislabelAbortAfterUpstream(t *testing.T) {
	repo := &auditAdmissionRepoStub{}
	svc := newReadyAuditAdmissionService(t, repo)
	router := gin.New()
	router.POST("/v1/messages", CoreGatewayRequestID(), setAuditAdmissionAPIKey,
		CoreGatewayAuditAdmission(svc, service.ProtocolProfileAnthropicMessagesV1),
		func(c *gin.Context) {
			// This represents a handler abort after an upstream attempt. An abort
			// alone is not evidence of a pre-upstream model rejection.
			c.AbortWithStatus(http.StatusBadGateway)
		})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"synthetic-model"}`)))
	require.Equal(t, http.StatusBadGateway, w.Code)
	require.Zero(t, repo.casCalls)
}

func TestAuditAdmissionDisabledMissingKeyAndMissingAuthenticationFailClosed(t *testing.T) {
	tests := []struct {
		name         string
		service      func() *service.AuditFoundationService
		authenticate gin.HandlerFunc
		wantStatus   int
	}{
		{name: "disabled", service: func() *service.AuditFoundationService {
			svc := service.NewAuditFoundationService(nil, config.AuditConfig{Mode: service.AuditModeDisabled})
			svc.Start()
			return svc
		}, authenticate: setAuditAdmissionAPIKey, wantStatus: http.StatusServiceUnavailable},
		{name: "missing audit key", service: func() *service.AuditFoundationService {
			svc := service.NewAuditFoundationService(&auditAdmissionRepoStub{}, config.AuditConfig{Mode: service.AuditModeRequired, ContentKeyRef: "env:AUDIT_ADMISSION_MISSING_AUDIT_KEY", ContentKeyVersion: "v1"})
			svc.Start()
			return svc
		}, authenticate: setAuditAdmissionAPIKey, wantStatus: http.StatusServiceUnavailable},
		{name: "missing api key", service: func() *service.AuditFoundationService {
			return newReadyAuditAdmissionService(t, &auditAdmissionRepoStub{})
		}, authenticate: func(c *gin.Context) { c.Next() }, wantStatus: http.StatusUnauthorized},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := tc.service()
			t.Cleanup(svc.Stop)
			upstream := 0
			router := gin.New()
			router.POST("/v1/responses", CoreGatewayRequestID(), tc.authenticate,
				CoreGatewayAuditAdmission(svc, service.ProtocolProfileOpenAIResponsesV1),
				func(c *gin.Context) { upstream++ })
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"synthetic"}`)))
			require.Equal(t, tc.wantStatus, w.Code)
			require.Zero(t, upstream)
		})
	}
}

func TestAuditAdmissionRepositoryFailureFailsClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &auditAdmissionRepoStub{admitErr: errors.New("synthetic capacity unavailable")}
	svc := newReadyAuditAdmissionService(t, repo)
	router := gin.New()
	upstream := 0
	router.POST("/v1/responses", CoreGatewayRequestID(), func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), &service.APIKey{ID: 8, User: &service.User{ID: 43, Status: service.StatusActive}})
		c.Next()
	}, CoreGatewayAuditAdmission(svc, service.ProtocolProfileOpenAIResponsesV1), func(c *gin.Context) { upstream++ })
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader([]byte(`{"model":"synthetic","input":"plaintext-sentinel"}`)))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.Zero(t, upstream)
	require.Contains(t, w.Body.String(), "gateway_audit_unavailable")
	id, err := uuid.Parse(w.Header().Get(GatewayRequestIDHeader))
	require.NoError(t, err)
	require.Equal(t, id.String(), gjsonString(w.Body.Bytes(), "gateway_request_id"))
	require.NotContains(t, w.Body.String(), "capacity")
	require.NotContains(t, w.Body.String(), "plaintext-sentinel")
}

func gjsonString(body []byte, path string) string {
	var value map[string]any
	_ = json.Unmarshal(body, &value)
	result, _ := value[path].(string)
	return result
}
