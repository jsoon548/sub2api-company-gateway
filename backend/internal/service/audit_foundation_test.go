package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type auditFoundationRepoStub struct {
	mu             sync.Mutex
	preflightErr   error
	reconcileErr   error
	reconcileCount int64
	casApplied     bool
	admitErr       error
	admitted       *AuditInteractionRecord
	admittedPart   *AuditContentPartRecord
}

func (s *auditFoundationRepoStub) CheckFoundation(context.Context) error { return s.preflightErr }
func (s *auditFoundationRepoStub) CreateInteraction(context.Context, AuditInteractionRecord) error {
	return nil
}
func (s *auditFoundationRepoStub) AppendEncryptedPart(context.Context, AuditContentPartRecord) error {
	return nil
}
func (s *auditFoundationRepoStub) AdmitRequest(_ context.Context, interaction AuditInteractionRecord, part AuditContentPartRecord) error {
	s.admitted = &interaction
	s.admittedPart = &part
	return s.admitErr
}
func (s *auditFoundationRepoStub) CommitResponsePart(context.Context, AuditResponsePartCommit) error {
	return nil
}
func (s *auditFoundationRepoStub) SetResponseWriteResult(context.Context, AuditResponseWriteResult) error {
	return nil
}
func (s *auditFoundationRepoStub) FinalizeInteraction(context.Context, AuditInteractionFinalization) error {
	return nil
}
func (s *auditFoundationRepoStub) CompareAndSwapRequestOutcome(context.Context, AuditStateCAS) (bool, error) {
	return s.casApplied, nil
}
func (s *auditFoundationRepoStub) CompareAndSwapContentState(context.Context, AuditStateCAS) (bool, error) {
	return s.casApplied, nil
}
func (s *auditFoundationRepoStub) ReconcileStale(context.Context, time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reconcileCount, s.reconcileErr
}

func TestAuditStateMachinesAcceptOnlyFrozenTransitions(t *testing.T) {
	requestStates := []string{AuditRequestProcessing, AuditRequestRejectedPreUpstream, AuditRequestCompleted, AuditRequestUpstreamFailed, AuditRequestInterrupted}
	requestLegal := map[string]bool{
		"processing->rejected_pre_upstream": true,
		"processing->completed":             true,
		"processing->upstream_failed":       true,
		"processing->interrupted":           true,
	}
	for _, from := range requestStates {
		for _, to := range requestStates {
			require.Equal(t, requestLegal[from+"->"+to], validAuditRequestTransition(from, to), "%s -> %s", from, to)
		}
	}

	contentStates := []string{AuditContentRecording, AuditContentComplete, AuditContentIncomplete, AuditContentExpired}
	contentLegal := map[string]bool{
		"recording->complete":   true,
		"recording->incomplete": true,
		"complete->expired":     true,
		"incomplete->expired":   true,
	}
	for _, from := range contentStates {
		for _, to := range contentStates {
			require.Equal(t, contentLegal[from+"->"+to], validAuditContentTransition(from, to), "%s -> %s", from, to)
		}
	}
}

func TestAuditStateServiceReportsCASConflictAndRejectsResurrection(t *testing.T) {
	repo := &auditFoundationRepoStub{casApplied: false}
	svc := NewAuditFoundationService(repo, config.AuditConfig{Mode: AuditModeDisabled})
	change := AuditStateCAS{InteractionID: uuid.New(), ExpectedState: AuditRequestProcessing, ExpectedVersion: 0, NextState: AuditRequestCompleted}
	require.ErrorIs(t, svc.AdvanceRequestOutcome(context.Background(), change), ErrAuditCASConflict)

	change.ExpectedState = AuditRequestCompleted
	change.ExpectedVersion = 1
	change.NextState = AuditRequestProcessing
	require.ErrorIs(t, svc.AdvanceRequestOutcome(context.Background(), change), ErrAuditInvalidTransition)
}

func TestAuditFoundationStatusMatrixConnectsAdmissionOnlyWhenRequiredReady(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		svc := NewAuditFoundationService(nil, config.AuditConfig{Mode: AuditModeDisabled})
		svc.Start()
		assertAuditStatus(t, svc.Status(), AuditModeDisabled, false, false, "disabled")
	})

	t.Run("required_ready", func(t *testing.T) {
		setSyntheticAuditKey(t, "AUDIT_AUDIT_READY_KEY", 32)
		svc := NewAuditFoundationService(&auditFoundationRepoStub{}, config.AuditConfig{
			Mode: AuditModeRequired, ContentKeyRef: "env:AUDIT_AUDIT_READY_KEY", ContentKeyVersion: "v1",
			ReconcileIntervalSeconds: 3600, ReconcileStaleAfterSeconds: 1,
		})
		svc.Start()
		defer svc.Stop()
		assertAuditStatus(t, svc.Status(), AuditModeRequired, true, true, "ready")
		_, ok := svc.Codec()
		require.True(t, ok)
	})

	t.Run("required_missing_key", func(t *testing.T) {
		svc := NewAuditFoundationService(&auditFoundationRepoStub{}, config.AuditConfig{
			Mode: AuditModeRequired, ContentKeyRef: "env:AUDIT_AUDIT_MISSING_KEY", ContentKeyVersion: "v1",
		})
		svc.Start()
		assertAuditStatus(t, svc.Status(), AuditModeRequired, false, false, "content_key_unavailable")
	})

	t.Run("required_missing_schema", func(t *testing.T) {
		setSyntheticAuditKey(t, "AUDIT_AUDIT_SCHEMA_KEY", 32)
		svc := NewAuditFoundationService(&auditFoundationRepoStub{preflightErr: ErrAuditSchemaNotReady}, config.AuditConfig{
			Mode: AuditModeRequired, ContentKeyRef: "env:AUDIT_AUDIT_SCHEMA_KEY", ContentKeyVersion: "v1", ReconcileIntervalSeconds: 3600,
		})
		svc.Start()
		defer svc.Stop()
		assertAuditStatus(t, svc.Status(), AuditModeRequired, false, false, "schema_not_ready")
	})

	t.Run("required_database_preflight_failed", func(t *testing.T) {
		setSyntheticAuditKey(t, "AUDIT_AUDIT_DB_KEY", 32)
		svc := NewAuditFoundationService(&auditFoundationRepoStub{preflightErr: errors.New("synthetic db error")}, config.AuditConfig{
			Mode: AuditModeRequired, ContentKeyRef: "env:AUDIT_AUDIT_DB_KEY", ContentKeyVersion: "v1", ReconcileIntervalSeconds: 3600,
		})
		svc.Start()
		defer svc.Stop()
		assertAuditStatus(t, svc.Status(), AuditModeRequired, false, false, "database_preflight_failed")
	})
}

func TestAuditFoundationLogsNeverExposeSecretReference(t *testing.T) {
	const ref = "AUDIT_AUDIT_HIGH_SENSITIVITY_REFERENCE"
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)

	svc := NewAuditFoundationService(&auditFoundationRepoStub{}, config.AuditConfig{
		Mode: AuditModeRequired, ContentKeyRef: "env:" + ref, ContentKeyVersion: "v1",
	})
	svc.Start()
	require.NotContains(t, logs.String(), ref)
	require.NotContains(t, strings.ToLower(logs.String()), "content_key_ref")
}

func TestAuditContentKeyReferenceRejectsKnownNonAuditSecrets(t *testing.T) {
	value := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x44}, 32))
	for _, name := range []string{"JWT_SECRET", "AUDIT_TOTP_KEY", "AUDIT_PAYMENT_KEY", "AUDIT_PROVIDER_KEY", "AUDIT_EMPLOYEE_PEPPER"} {
		t.Setenv(name, value)
		_, err := resolveAuditContentKey("env:" + name)
		require.ErrorIs(t, err, ErrAuditSecretUnavailable, name)
	}
}

func TestAuditAdmissionEncryptsAndUsesAtomicRepositoryPort(t *testing.T) {
	setSyntheticAuditKey(t, "AUDIT_ADMISSION_AUDIT_ADMISSION_KEY", 32)
	repo := &auditFoundationRepoStub{}
	svc := NewAuditFoundationService(repo, config.AuditConfig{Mode: AuditModeRequired, ContentKeyRef: "env:AUDIT_ADMISSION_AUDIT_ADMISSION_KEY", ContentKeyVersion: "v1", ReconcileIntervalSeconds: 3600})
	svc.Start()
	defer svc.Stop()
	plaintext := []byte(`{"request_uri":"/v1/messages?synthetic=1","body":"synthetic-auditAdmission"}`)
	result, err := svc.AdmitRequest(context.Background(), AuditAdmissionInput{GatewayRequestID: uuid.New(), ProfileVersion: ProtocolProfileAnthropicMessagesV1, Protocol: "anthropic", Endpoint: "/v1/messages", Method: "POST", Transport: "http", Plaintext: plaintext})
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, result.InteractionID)
	require.NotNil(t, repo.admitted)
	require.NotNil(t, repo.admittedPart)
	require.Equal(t, 1, repo.admitted.RequestPartCount)
	require.NotContains(t, string(repo.admittedPart.Encrypted.Ciphertext), "synthetic-auditAdmission")
	codec, ok := svc.Codec()
	require.True(t, ok)
	decoded, err := codec.Decrypt(AuditPartAAD{InteractionID: result.InteractionID, GatewayRequestID: result.GatewayRequestID, Direction: "request", Sequence: 0, AdmittedAt: result.AdmittedAt, KeyVersion: "v1"}, repo.admittedPart.Encrypted)
	require.NoError(t, err)
	require.Equal(t, plaintext, decoded)
}

func assertAuditStatus(t *testing.T, status AuditFoundationStatus, mode string, ready, enabled bool, reason string) {
	t.Helper()
	require.Equal(t, mode, status.Mode)
	require.Equal(t, ready, status.FoundationReady)
	require.Equal(t, enabled, status.AdmissionConnected)
	require.Equal(t, enabled, status.GatewayContentEnabled)
	require.Equal(t, reason, status.ReasonCode)
}

func setSyntheticAuditKey(t *testing.T, name string, size int) {
	t.Helper()
	t.Setenv(name, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x5a}, size)))
}
