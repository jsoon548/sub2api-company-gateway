package service

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

const (
	auditRetentionBatchSize = 100
	auditRetentionInterval  = time.Hour
)

var (
	ErrAuditDisclosureForbidden   = infraerrors.Forbidden("AUDIT_DISCLOSURE_FORBIDDEN", "current super administrator named session required")
	ErrAuditContentUnavailable    = infraerrors.NotFound("AUDIT_CONTENT_UNAVAILABLE", "audit content is unavailable")
	ErrAuditDisclosureNotReady    = infraerrors.Conflict("AUDIT_DISCLOSURE_NOT_READY", "audit content is not ready for disclosure")
	ErrAuditGovernanceUnavailable = infraerrors.ServiceUnavailable("AUDIT_GOVERNANCE_UNAVAILABLE", "audit disclosure governance is unavailable")
)

// AuditMetadataFilter is intentionally limited to structured metadata. No raw
// content or ciphertext search surface is exposed.
type AuditMetadataFilter struct {
	Employee         string
	From             *time.Time
	To               *time.Time
	Protocol         string
	Model            string
	RequestOutcome   string
	ContentState     string
	GatewayRequestID *uuid.UUID
	Page             int
	PageSize         int
}

// AuditMetadataRecord is the only interaction shape returned by management
// list and disclosure APIs. It deliberately omits API-key references, hashes,
// encryption metadata, nonces, ciphertext, and authentication tags.
type AuditMetadataRecord struct {
	ID                    uuid.UUID  `json:"id"`
	GatewayRequestID      uuid.UUID  `json:"gateway_request_id"`
	SubjectUserID         *int64     `json:"subject_user_id,omitempty"`
	SubjectEmailSnapshot  *string    `json:"subject_email_snapshot,omitempty"`
	ProfileVersion        string     `json:"profile_version"`
	Protocol              string     `json:"protocol"`
	Endpoint              string     `json:"endpoint"`
	Method                string     `json:"method"`
	Transport             string     `json:"transport"`
	RequestedModel        *string    `json:"requested_model,omitempty"`
	ResolvedModel         *string    `json:"resolved_model,omitempty"`
	RequestOutcome        string     `json:"request_outcome"`
	ContentState          string     `json:"content_state"`
	DownstreamStatus      *int       `json:"downstream_status,omitempty"`
	DownstreamWriteResult string     `json:"downstream_write_result"`
	AdmittedAt            time.Time  `json:"admitted_at"`
	CompletedAt           *time.Time `json:"completed_at,omitempty"`
	ExpiresAt             time.Time  `json:"expires_at"`
	LastActivityAt        time.Time  `json:"last_activity_at"`
	RequestPartCount      int        `json:"request_part_count"`
	ResponsePartCount     int        `json:"response_part_count"`
	SafeErrorSummary      *string    `json:"safe_error_summary,omitempty"`
}

type AuditMetadataPage struct {
	Items    []AuditMetadataRecord `json:"items"`
	Total    int64                 `json:"total"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
}

type AuditDisclosureActor struct {
	UserID           int64
	SessionVersion   int64
	SessionExpiresAt time.Time
	Role             string
	AuthMethod       string
}

type AuditDisclosureInput struct {
	InteractionID uuid.UUID
	Actor         AuditDisclosureActor
}

type RawAuditContentPart struct {
	Direction string `json:"direction"`
	Sequence  int    `json:"sequence"`
	Content   string `json:"content"`
}

type AuditDisclosureResult struct {
	OperationID uuid.UUID             `json:"operation_id"`
	Metadata    AuditMetadataRecord   `json:"metadata"`
	Parts       []RawAuditContentPart `json:"parts"`
}

type AuditDisclosureMaterialPart struct {
	Direction string
	Sequence  int
	Encrypted EncryptedAuditPart
}

type AuditDisclosureMaterial struct {
	Metadata AuditMetadataRecord
	Parts    []AuditDisclosureMaterialPart
}

type AuditRetentionResult struct {
	Candidates int `json:"candidates"`
	Purged     int `json:"purged"`
	Failed     int `json:"failed"`
}

type AuditManagementRepository interface {
	ListAuditMetadata(context.Context, AuditMetadataFilter) (AuditMetadataPage, error)
	RecordDisclosureStarted(context.Context, uuid.UUID, AuditDisclosureActor, uuid.UUID) error
	LoadDisclosureMaterial(context.Context, uuid.UUID) (AuditDisclosureMaterial, error)
	RecordDisclosureCompleted(context.Context, uuid.UUID, AuditDisclosureActor, uuid.UUID, bool, string) error
	PurgeExpiredAuditContent(context.Context, time.Time, int) (AuditRetentionResult, error)
}

// AuditManagementService owns audit management's metadata, controlled disclosure, and
// online-retention boundary. The retention worker does not depend on key
// availability, while disclosure does.
type AuditManagementService struct {
	repo             AuditManagementRepository
	gatewayUsageRepo GatewayUsageRepository
	foundation       *AuditFoundationService
	now              func() time.Time
	interval         time.Duration
	batchSize        int
	startOnce        sync.Once
	stopOnce         sync.Once
	stopCh           chan struct{}
	doneCh           chan struct{}
}

func NewAuditManagementService(repo AuditManagementRepository, foundation *AuditFoundationService) *AuditManagementService {
	service := &AuditManagementService{
		repo: repo, foundation: foundation, now: time.Now,
		interval: auditRetentionInterval, batchSize: auditRetentionBatchSize,
		stopCh: make(chan struct{}), doneCh: make(chan struct{}),
	}
	if gatewayUsageRepo, ok := repo.(GatewayUsageRepository); ok {
		service.gatewayUsageRepo = gatewayUsageRepo
	}
	return service
}

func (s *AuditManagementService) Start() {
	if s == nil {
		return
	}
	s.startOnce.Do(func() {
		go s.retentionLoop()
	})
}

func (s *AuditManagementService) Stop() {
	if s == nil {
		return
	}
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

func (s *AuditManagementService) ListMetadata(ctx context.Context, filter AuditMetadataFilter) (AuditMetadataPage, error) {
	if s == nil || s.repo == nil {
		return AuditMetadataPage{}, errors.New("audit management repository unavailable")
	}
	filter.Employee = strings.TrimSpace(filter.Employee)
	filter.Protocol = strings.ToLower(strings.TrimSpace(filter.Protocol))
	filter.Model = strings.TrimSpace(filter.Model)
	filter.RequestOutcome = strings.ToLower(strings.TrimSpace(filter.RequestOutcome))
	filter.ContentState = strings.ToLower(strings.TrimSpace(filter.ContentState))
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 {
		filter.PageSize = 20
	}
	if filter.PageSize > 100 {
		filter.PageSize = 100
	}
	if len(filter.Employee) > 255 || len(filter.Model) > 100 ||
		!validOptionalValue(filter.Protocol, "anthropic", "openai") ||
		!validOptionalValue(filter.RequestOutcome, AuditRequestProcessing, AuditRequestRejectedPreUpstream, AuditRequestCompleted, AuditRequestUpstreamFailed, AuditRequestInterrupted) ||
		!validOptionalValue(filter.ContentState, AuditContentRecording, AuditContentComplete, AuditContentIncomplete, AuditContentExpired) ||
		(filter.From != nil && filter.To != nil && filter.From.After(*filter.To)) {
		return AuditMetadataPage{}, infraerrors.BadRequest("AUDIT_FILTER_INVALID", "invalid audit metadata filter")
	}
	return s.repo.ListAuditMetadata(ctx, filter)
}

func (s *AuditManagementService) Disclose(ctx context.Context, input AuditDisclosureInput) (AuditDisclosureResult, error) {
	if s == nil || s.repo == nil || s.foundation == nil {
		return AuditDisclosureResult{}, ErrAuditGovernanceUnavailable
	}
	if input.InteractionID == uuid.Nil {
		return AuditDisclosureResult{}, ErrAuditContentUnavailable
	}
	if input.Actor.UserID <= 0 || input.Actor.SessionVersion < 0 || input.Actor.SessionExpiresAt.IsZero() ||
		!input.Actor.SessionExpiresAt.After(s.now().UTC()) || input.Actor.AuthMethod != "jwt" ||
		!(Authorizer{Role: input.Actor.Role}).Has(CapabilityRawContentDisclosure) {
		return AuditDisclosureResult{}, ErrAuditDisclosureForbidden
	}
	codec, ok := s.foundation.Codec()
	if !ok {
		return AuditDisclosureResult{}, ErrAuditContentUnavailable
	}

	operationID := uuid.New()
	if err := s.repo.RecordDisclosureStarted(ctx, operationID, input.Actor, input.InteractionID); err != nil {
		return AuditDisclosureResult{}, err
	}

	fail := func(safeSummary string, cause error) (AuditDisclosureResult, error) {
		completionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if eventErr := s.repo.RecordDisclosureCompleted(completionCtx, operationID, input.Actor, input.InteractionID, false, safeSummary); eventErr != nil {
			if errors.Is(eventErr, ErrAuditDisclosureForbidden) {
				return AuditDisclosureResult{}, eventErr
			}
			return AuditDisclosureResult{}, ErrAuditGovernanceUnavailable.WithCause(eventErr)
		}
		if cause == nil {
			cause = ErrAuditContentUnavailable
		}
		return AuditDisclosureResult{}, cause
	}

	material, err := s.repo.LoadDisclosureMaterial(ctx, input.InteractionID)
	if err != nil {
		return fail("content_load_failed", err)
	}
	parts := make([]RawAuditContentPart, 0, len(material.Parts))
	for _, part := range material.Parts {
		plaintext, decryptErr := codec.Decrypt(AuditPartAAD{
			InteractionID: material.Metadata.ID, GatewayRequestID: material.Metadata.GatewayRequestID,
			Direction: part.Direction, Sequence: part.Sequence, AdmittedAt: material.Metadata.AdmittedAt,
			KeyVersion: part.Encrypted.KeyVersion,
		}, part.Encrypted)
		if decryptErr != nil {
			return fail("content_decryption_failed", ErrAuditContentUnavailable)
		}
		if !utf8.Valid(plaintext) {
			for i := range plaintext {
				plaintext[i] = 0
			}
			return fail("content_encoding_invalid", ErrAuditContentUnavailable)
		}
		parts = append(parts, RawAuditContentPart{Direction: part.Direction, Sequence: part.Sequence, Content: string(plaintext)})
		for i := range plaintext {
			plaintext[i] = 0
		}
	}
	sort.SliceStable(parts, func(i, j int) bool {
		if parts[i].Direction != parts[j].Direction {
			return parts[i].Direction == "request"
		}
		return parts[i].Sequence < parts[j].Sequence
	})
	completionCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := s.repo.RecordDisclosureCompleted(completionCtx, operationID, input.Actor, input.InteractionID, true, ""); err != nil {
		for i := range parts {
			parts[i].Content = ""
		}
		if errors.Is(err, ErrAuditDisclosureForbidden) || errors.Is(err, ErrAuditContentUnavailable) {
			return AuditDisclosureResult{}, err
		}
		return AuditDisclosureResult{}, ErrAuditGovernanceUnavailable.WithCause(err)
	}
	return AuditDisclosureResult{OperationID: operationID, Metadata: material.Metadata, Parts: parts}, nil
}

func (s *AuditManagementService) RunRetention(ctx context.Context, cutoff time.Time) (AuditRetentionResult, error) {
	if s == nil || s.repo == nil {
		return AuditRetentionResult{}, errors.New("audit management repository unavailable")
	}
	if cutoff.IsZero() {
		cutoff = s.now().UTC()
	}
	batchSize := s.batchSize
	if batchSize <= 0 {
		batchSize = auditRetentionBatchSize
	}
	return s.repo.PurgeExpiredAuditContent(ctx, cutoff.UTC(), batchSize)
}

func (s *AuditManagementService) retentionLoop() {
	defer close(s.doneCh)
	s.runScheduledRetention()
	interval := s.interval
	if interval <= 0 {
		interval = auditRetentionInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.runScheduledRetention()
		case <-s.stopCh:
			return
		}
	}
}

func (s *AuditManagementService) runScheduledRetention() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := s.RunRetention(ctx, s.now().UTC())
	if err != nil {
		slog.Error("audit retention cleanup failed", "error_class", "database_unavailable")
		return
	}
	if result.Candidates > 0 || result.Failed > 0 {
		slog.Info("audit retention cleanup completed", "candidates", result.Candidates, "purged", result.Purged, "failed", result.Failed)
	}
}

func validOptionalValue(value string, allowed ...string) bool {
	if value == "" {
		return true
	}
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
