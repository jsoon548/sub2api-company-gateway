package service

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
)

const (
	AuditModeDisabled = "disabled"
	AuditModeRequired = "required"

	AuditRequestProcessing          = "processing"
	AuditRequestRejectedPreUpstream = "rejected_pre_upstream"
	AuditRequestCompleted           = "completed"
	AuditRequestUpstreamFailed      = "upstream_failed"
	AuditRequestInterrupted         = "interrupted"

	AuditContentRecording  = "recording"
	AuditContentComplete   = "complete"
	AuditContentIncomplete = "incomplete"
	AuditContentExpired    = "expired"
)

var (
	ErrAuditInvalidTransition = errors.New("invalid audit state transition")
	ErrAuditCASConflict       = errors.New("audit state compare-and-swap conflict")
	ErrAuditSchemaNotReady    = errors.New("audit foundation schema is not ready")
	ErrAuditSecretUnavailable = errors.New("audit content key is unavailable")
)

var auditSecretEnvName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

type AuditInteractionRecord struct {
	ID                   uuid.UUID
	GatewayRequestID     uuid.UUID
	SubjectUserID        *int64
	SubjectEmailSnapshot *string
	APIKeyID             *int64
	APIKeyFingerprint    *string
	ProfileVersion       string
	Protocol             string
	Endpoint             string
	Method               string
	Transport            string
	RequestedModel       *string
	AdmittedAt           time.Time
	ExpiresAt            time.Time
	LastActivityAt       time.Time
	RequestSHA256        []byte
	RequestPartCount     int
}

type AuditContentPartRecord struct {
	ID                    uuid.UUID
	InteractionID         uuid.UUID
	Direction             string
	Sequence              int
	Encrypted             EncryptedAuditPart
	DownstreamWriteResult string
	CreatedAt             time.Time
}

// AuditResponsePartCommit is the atomic durability boundary for one final
// Gateway response batch. ResponseSHA256 is the digest of all final body bytes
// from sequence zero through this batch, not a digest of an upstream payload.
type AuditResponsePartCommit struct {
	Part              AuditContentPartRecord
	ExpectedPartCount int
	ResponseSHA256    []byte
	DownstreamStatus  int
	At                time.Time
}

type AuditResponseWriteResult struct {
	InteractionID uuid.UUID
	PartID        uuid.UUID
	Sequence      int
	Result        string
	At            time.Time
}

// AuditInteractionFinalization advances both state machines together. A
// failed finalization must leave processing/recording intact for reconciliation
// rather than advertising a partially completed interaction.
type AuditInteractionFinalization struct {
	InteractionID    uuid.UUID
	RequestOutcome   string
	ContentState     string
	WriteResult      string
	At               time.Time
	SafeErrorSummary *string
}

type AuditStateCAS struct {
	InteractionID    uuid.UUID
	ExpectedState    string
	ExpectedVersion  int64
	NextState        string
	At               time.Time
	SafeErrorSummary *string
}

// AuditFoundationRepository owns the PostgreSQL audit foundation and the
// atomic audit admission admission write.
type AuditFoundationRepository interface {
	CheckFoundation(context.Context) error
	CreateInteraction(context.Context, AuditInteractionRecord) error
	AppendEncryptedPart(context.Context, AuditContentPartRecord) error
	AdmitRequest(context.Context, AuditInteractionRecord, AuditContentPartRecord) error
	CommitResponsePart(context.Context, AuditResponsePartCommit) error
	SetResponseWriteResult(context.Context, AuditResponseWriteResult) error
	FinalizeInteraction(context.Context, AuditInteractionFinalization) error
	CompareAndSwapRequestOutcome(context.Context, AuditStateCAS) (bool, error)
	CompareAndSwapContentState(context.Context, AuditStateCAS) (bool, error)
	ReconcileStale(context.Context, time.Time) (int64, error)
}

type AuditAdmissionInput struct {
	GatewayRequestID     uuid.UUID
	SubjectUserID        *int64
	SubjectEmailSnapshot *string
	APIKeyID             *int64
	ProfileVersion       string
	Protocol             string
	Endpoint             string
	Method               string
	Transport            string
	RequestedModel       *string
	Plaintext            []byte
}

type AuditAdmissionResult struct {
	InteractionID    uuid.UUID
	GatewayRequestID uuid.UUID
	AdmittedAt       time.Time
}

type AuditFoundationStatus struct {
	Mode                  string `json:"mode"`
	FoundationReady       bool   `json:"foundation_ready"`
	AdmissionConnected    bool   `json:"admission_connected"`
	GatewayContentEnabled bool   `json:"gateway_content_enabled"`
	ReasonCode            string `json:"reason_code"`
}

// AuditFoundationService owns preflight, admission, state-machine validation,
// and stale reconciliation.
type AuditFoundationService struct {
	repo      AuditFoundationRepository
	cfg       config.AuditConfig
	codec     atomic.Pointer[AuditPartCodec]
	status    atomic.Value
	startOnce sync.Once
	stopOnce  sync.Once
	stopCh    chan struct{}
	doneCh    chan struct{}
}

func NewAuditFoundationService(repo AuditFoundationRepository, cfg config.AuditConfig) *AuditFoundationService {
	s := &AuditFoundationService{
		repo:   repo,
		cfg:    cfg,
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.Mode))
	s.status.Store(AuditFoundationStatus{Mode: mode, ReasonCode: "not_started"})
	return s
}

func (s *AuditFoundationService) Start() {
	s.startOnce.Do(func() {
		mode := strings.ToLower(strings.TrimSpace(s.cfg.Mode))
		if mode == AuditModeDisabled {
			s.setStatus(mode, false, "disabled")
			close(s.doneCh)
			return
		}
		if mode != AuditModeRequired {
			s.setStatus(mode, false, "invalid_mode")
			close(s.doneCh)
			return
		}

		key, err := resolveAuditContentKey(s.cfg.ContentKeyRef)
		if err != nil {
			s.setStatus(mode, false, "content_key_unavailable")
			close(s.doneCh)
			return
		}
		codec, err := NewAuditPartCodec(key, strings.TrimSpace(s.cfg.ContentKeyVersion))
		for i := range key {
			key[i] = 0
		}
		if err != nil {
			s.setStatus(mode, false, "content_key_invalid")
			close(s.doneCh)
			return
		}
		s.codec.Store(codec)
		s.runPreflightAndReconcile()
		go s.reconcileLoop()
	})
}

func (s *AuditFoundationService) Stop() {
	s.stopOnce.Do(func() {
		select {
		case <-s.doneCh:
			return
		default:
			close(s.stopCh)
			<-s.doneCh
		}
	})
}

func (s *AuditFoundationService) Status() AuditFoundationStatus {
	return s.status.Load().(AuditFoundationStatus)
}

func (s *AuditFoundationService) Codec() (*AuditPartCodec, bool) {
	codec := s.codec.Load()
	return codec, codec != nil && s.Status().FoundationReady
}

// AdmitRequest encrypts the exact captured request envelope and atomically
// commits the interaction plus its first request part before any upstream work.
func (s *AuditFoundationService) AdmitRequest(ctx context.Context, input AuditAdmissionInput) (AuditAdmissionResult, error) {
	if s == nil || s.repo == nil || !s.Status().GatewayContentEnabled {
		return AuditAdmissionResult{}, ErrAuditSchemaNotReady
	}
	codec, ok := s.Codec()
	if !ok || input.GatewayRequestID == uuid.Nil || len(input.Plaintext) == 0 {
		return AuditAdmissionResult{}, ErrAuditSecretUnavailable
	}
	now := time.Now().UTC()
	interactionID := uuid.New()
	encrypted, err := codec.Encrypt(AuditPartAAD{
		InteractionID: interactionID, GatewayRequestID: input.GatewayRequestID,
		Direction: "request", Sequence: 0, AdmittedAt: now,
		KeyVersion: strings.TrimSpace(s.cfg.ContentKeyVersion),
	}, input.Plaintext)
	if err != nil {
		return AuditAdmissionResult{}, err
	}
	interaction := AuditInteractionRecord{
		ID: interactionID, GatewayRequestID: input.GatewayRequestID,
		SubjectUserID: input.SubjectUserID, SubjectEmailSnapshot: input.SubjectEmailSnapshot,
		APIKeyID: input.APIKeyID, ProfileVersion: input.ProfileVersion,
		Protocol: input.Protocol, Endpoint: input.Endpoint, Method: input.Method,
		Transport: input.Transport, RequestedModel: input.RequestedModel,
		AdmittedAt: now, ExpiresAt: now.Add(180 * 24 * time.Hour), LastActivityAt: now,
		RequestSHA256: append([]byte(nil), encrypted.PlaintextSHA256...), RequestPartCount: 1,
	}
	part := AuditContentPartRecord{
		ID: uuid.New(), InteractionID: interactionID, Direction: "request", Sequence: 0,
		Encrypted: encrypted, DownstreamWriteResult: "not_applicable", CreatedAt: now,
	}
	if err := s.repo.AdmitRequest(ctx, interaction, part); err != nil {
		return AuditAdmissionResult{}, err
	}
	return AuditAdmissionResult{InteractionID: interactionID, GatewayRequestID: input.GatewayRequestID, AdmittedAt: now}, nil
}

// CommitResponsePart encrypts one exact, final Gateway response envelope and
// commits it with the cumulative raw-body hash/count/status before downstream
// output is attempted.
func (s *AuditFoundationService) CommitResponsePart(ctx context.Context, admission AuditAdmissionResult, sequence int, plaintext, responseSHA256 []byte, downstreamStatus int) (uuid.UUID, error) {
	if s == nil || s.repo == nil || !s.Status().GatewayContentEnabled {
		return uuid.Nil, ErrAuditSchemaNotReady
	}
	codec, ok := s.Codec()
	if !ok || admission.InteractionID == uuid.Nil || admission.GatewayRequestID == uuid.Nil || admission.AdmittedAt.IsZero() || sequence < 0 || len(plaintext) == 0 || len(responseSHA256) != 32 || downstreamStatus < 100 || downstreamStatus > 599 {
		return uuid.Nil, ErrAuditSecretUnavailable
	}
	now := time.Now().UTC()
	encrypted, err := codec.Encrypt(AuditPartAAD{
		InteractionID: admission.InteractionID, GatewayRequestID: admission.GatewayRequestID,
		Direction: "response", Sequence: sequence, AdmittedAt: admission.AdmittedAt,
		KeyVersion: strings.TrimSpace(s.cfg.ContentKeyVersion),
	}, plaintext)
	if err != nil {
		return uuid.Nil, err
	}
	partID := uuid.New()
	commit := AuditResponsePartCommit{
		Part: AuditContentPartRecord{
			ID: partID, InteractionID: admission.InteractionID, Direction: "response", Sequence: sequence,
			Encrypted: encrypted, DownstreamWriteResult: "pending", CreatedAt: now,
		},
		ExpectedPartCount: sequence, ResponseSHA256: append([]byte(nil), responseSHA256...),
		DownstreamStatus: downstreamStatus, At: now,
	}
	if err := s.repo.CommitResponsePart(ctx, commit); err != nil {
		return uuid.Nil, err
	}
	return partID, nil
}

func (s *AuditFoundationService) SetResponseWriteResult(ctx context.Context, result AuditResponseWriteResult) error {
	if result.Result != "succeeded" && result.Result != "failed" && result.Result != "unknown" {
		return ErrAuditInvalidTransition
	}
	if result.InteractionID == uuid.Nil || result.PartID == uuid.Nil || result.Sequence < 0 {
		return ErrAuditInvalidTransition
	}
	return s.repo.SetResponseWriteResult(ctx, result)
}

func (s *AuditFoundationService) FinalizeInteraction(ctx context.Context, final AuditInteractionFinalization) error {
	if final.InteractionID == uuid.Nil || !validAuditRequestTransition(AuditRequestProcessing, final.RequestOutcome) || !validAuditContentTransition(AuditContentRecording, final.ContentState) {
		return ErrAuditInvalidTransition
	}
	if final.WriteResult != "succeeded" && final.WriteResult != "failed" && final.WriteResult != "unknown" {
		return ErrAuditInvalidTransition
	}
	return s.repo.FinalizeInteraction(ctx, final)
}

func (s *AuditFoundationService) AdvanceRequestOutcome(ctx context.Context, change AuditStateCAS) error {
	if !validAuditRequestTransition(change.ExpectedState, change.NextState) {
		return ErrAuditInvalidTransition
	}
	applied, err := s.repo.CompareAndSwapRequestOutcome(ctx, change)
	if err != nil {
		return err
	}
	if !applied {
		return ErrAuditCASConflict
	}
	return nil
}

func (s *AuditFoundationService) AdvanceContentState(ctx context.Context, change AuditStateCAS) error {
	if !validAuditContentTransition(change.ExpectedState, change.NextState) {
		return ErrAuditInvalidTransition
	}
	applied, err := s.repo.CompareAndSwapContentState(ctx, change)
	if err != nil {
		return err
	}
	if !applied {
		return ErrAuditCASConflict
	}
	return nil
}

func (s *AuditFoundationService) ReconcileNow(ctx context.Context) (int64, error) {
	staleAfter := time.Duration(s.cfg.ReconcileStaleAfterSeconds) * time.Second
	if staleAfter <= 0 {
		staleAfter = 15 * time.Minute
	}
	return s.repo.ReconcileStale(ctx, time.Now().UTC().Add(-staleAfter))
}

func (s *AuditFoundationService) reconcileLoop() {
	defer close(s.doneCh)
	interval := time.Duration(s.cfg.ReconcileIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.runPreflightAndReconcile()
		case <-s.stopCh:
			return
		}
	}
}

func (s *AuditFoundationService) runPreflightAndReconcile() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.repo.CheckFoundation(ctx); err != nil {
		reason := "database_preflight_failed"
		if errors.Is(err, ErrAuditSchemaNotReady) {
			reason = "schema_not_ready"
		}
		s.setStatus(AuditModeRequired, false, reason)
		return
	}
	if _, err := s.ReconcileNow(ctx); err != nil {
		s.setStatus(AuditModeRequired, false, "reconciliation_failed")
		return
	}
	s.setStatus(AuditModeRequired, true, "ready")
}

func (s *AuditFoundationService) setStatus(mode string, ready bool, reason string) {
	enabled := mode == AuditModeRequired && ready
	status := AuditFoundationStatus{
		Mode:                  mode,
		FoundationReady:       ready,
		AdmissionConnected:    enabled,
		GatewayContentEnabled: enabled,
		ReasonCode:            reason,
	}
	s.status.Store(status)
	slog.Info("core audit foundation status",
		"mode", status.Mode,
		"foundation_ready", status.FoundationReady,
		"admission_connected", status.AdmissionConnected,
		"gateway_content_enabled", status.GatewayContentEnabled,
		"reason_code", status.ReasonCode,
	)
}

func validAuditRequestTransition(from, to string) bool {
	if from != AuditRequestProcessing {
		return false
	}
	switch to {
	case AuditRequestRejectedPreUpstream, AuditRequestCompleted, AuditRequestUpstreamFailed, AuditRequestInterrupted:
		return true
	default:
		return false
	}
}

func validAuditContentTransition(from, to string) bool {
	switch from {
	case AuditContentRecording:
		return to == AuditContentComplete || to == AuditContentIncomplete
	case AuditContentComplete, AuditContentIncomplete:
		return to == AuditContentExpired
	default:
		return false
	}
}

func resolveAuditContentKey(ref string) ([]byte, error) {
	const prefix = "env:"
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, prefix) {
		return nil, ErrAuditSecretUnavailable
	}
	name := strings.TrimPrefix(ref, prefix)
	upperName := strings.ToUpper(name)
	if !auditSecretEnvName.MatchString(name) || !strings.Contains(upperName, "AUDIT") {
		return nil, ErrAuditSecretUnavailable
	}
	for _, forbidden := range []string{"JWT", "TOTP", "PAYMENT", "PROVIDER", "PEPPER"} {
		if strings.Contains(upperName, forbidden) {
			return nil, ErrAuditSecretUnavailable
		}
	}
	if upperName == "AUDIT_CONTENT_KEY_REF" {
		return nil, ErrAuditSecretUnavailable
	}
	encoded, ok := os.LookupEnv(name)
	if !ok || strings.TrimSpace(encoded) == "" {
		return nil, ErrAuditSecretUnavailable
	}
	key, err := base64.StdEncoding.Strict().DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(key) != 32 {
		return nil, ErrAuditSecretUnavailable
	}
	return key, nil
}
