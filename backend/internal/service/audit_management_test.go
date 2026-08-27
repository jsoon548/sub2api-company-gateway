package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type auditManagementRepoStub struct {
	material             AuditDisclosureMaterial
	events               []string
	startErr             error
	loadErr              error
	completeErr          error
	cancelOnLoad         context.CancelFunc
	completionContextErr error
	retention            AuditRetentionResult
	cutoff               time.Time
	batchSize            int
}

func (s *auditManagementRepoStub) ListAuditMetadata(context.Context, AuditMetadataFilter) (AuditMetadataPage, error) {
	return AuditMetadataPage{}, nil
}

func (s *auditManagementRepoStub) RecordDisclosureStarted(_ context.Context, _ uuid.UUID, _ AuditDisclosureActor, _ uuid.UUID) error {
	s.events = append(s.events, "started")
	return s.startErr
}

func (s *auditManagementRepoStub) LoadDisclosureMaterial(context.Context, uuid.UUID) (AuditDisclosureMaterial, error) {
	s.events = append(s.events, "loaded")
	if s.cancelOnLoad != nil {
		s.cancelOnLoad()
	}
	if s.loadErr != nil {
		return AuditDisclosureMaterial{}, s.loadErr
	}
	return s.material, nil
}

func (s *auditManagementRepoStub) RecordDisclosureCompleted(ctx context.Context, _ uuid.UUID, _ AuditDisclosureActor, _ uuid.UUID, succeeded bool, _ string) error {
	s.completionContextErr = ctx.Err()
	if succeeded {
		s.events = append(s.events, "completed:succeeded")
	} else {
		s.events = append(s.events, "completed:failed")
	}
	return s.completeErr
}

func (s *auditManagementRepoStub) PurgeExpiredAuditContent(_ context.Context, cutoff time.Time, batchSize int) (AuditRetentionResult, error) {
	s.cutoff = cutoff
	s.batchSize = batchSize
	return s.retention, nil
}

func newReadyAuditManagement(t *testing.T, repo *auditManagementRepoStub) (*AuditManagementService, *AuditPartCodec) {
	t.Helper()
	const keyName = "AUDIT_MANAGEMENT_AUDIT_DISCLOSURE_KEY"
	key := bytes.Repeat([]byte{0x77}, 32)
	t.Setenv(keyName, base64.StdEncoding.EncodeToString(key))
	foundation := NewAuditFoundationService(&auditFoundationRepoStub{}, config.AuditConfig{
		Mode: AuditModeRequired, ContentKeyRef: "env:" + keyName,
		ContentKeyVersion: "auditManagement-v1", ReconcileIntervalSeconds: 3600,
	})
	foundation.Start()
	t.Cleanup(foundation.Stop)
	codec, ok := foundation.Codec()
	require.True(t, ok)
	return NewAuditManagementService(repo, foundation), codec
}

func syntheticDisclosureMaterial(t *testing.T, codec *AuditPartCodec) AuditDisclosureMaterial {
	t.Helper()
	admittedAt := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	interactionID, gatewayID := uuid.New(), uuid.New()
	plaintext := []byte(`{"version":"core-gateway-request-v1","body":"auditManagement-synthetic"}`)
	encrypted, err := codec.Encrypt(AuditPartAAD{
		InteractionID: interactionID, GatewayRequestID: gatewayID,
		Direction: "request", Sequence: 0, AdmittedAt: admittedAt, KeyVersion: "auditManagement-v1",
	}, plaintext)
	require.NoError(t, err)
	return AuditDisclosureMaterial{
		Metadata: AuditMetadataRecord{
			ID: interactionID, GatewayRequestID: gatewayID, ProfileVersion: ProtocolProfileOpenAIResponsesV1,
			Protocol: "openai", Endpoint: "/v1/responses", Method: "POST", Transport: "http",
			RequestOutcome: AuditRequestCompleted, ContentState: AuditContentComplete,
			DownstreamWriteResult: "succeeded", AdmittedAt: admittedAt,
			ExpiresAt: admittedAt.Add(180 * 24 * time.Hour), LastActivityAt: admittedAt,
			RequestPartCount: 1,
		},
		Parts: []AuditDisclosureMaterialPart{{Direction: "request", Sequence: 0, Encrypted: encrypted}},
	}
}

func TestAuditDisclosureRequiresCurrentNamedSuperAdmin(t *testing.T) {
	repo := &auditManagementRepoStub{}
	svc, codec := newReadyAuditManagement(t, repo)
	repo.material = syntheticDisclosureMaterial(t, codec)
	base := AuditDisclosureInput{
		InteractionID: repo.material.Metadata.ID,
		Actor:         AuditDisclosureActor{UserID: 7, SessionVersion: 3, SessionExpiresAt: time.Now().Add(time.Hour), Role: RoleSuperAdmin, AuthMethod: "jwt"},
	}

	for _, tc := range []struct {
		name   string
		mutate func(*AuditDisclosureInput)
		want   error
	}{
		{name: "ordinary admin", mutate: func(in *AuditDisclosureInput) { in.Actor.Role = RoleAdmin }, want: ErrAuditDisclosureForbidden},
		{name: "user", mutate: func(in *AuditDisclosureInput) { in.Actor.Role = RoleUser }, want: ErrAuditDisclosureForbidden},
		{name: "shared admin key", mutate: func(in *AuditDisclosureInput) { in.Actor.AuthMethod = "admin_api_key" }, want: ErrAuditDisclosureForbidden},
		{name: "expired named session", mutate: func(in *AuditDisclosureInput) { in.Actor.SessionExpiresAt = time.Now().Add(-time.Second) }, want: ErrAuditDisclosureForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := base
			tc.mutate(&input)
			before := len(repo.events)
			_, err := svc.Disclose(context.Background(), input)
			require.ErrorIs(t, err, tc.want)
			require.Len(t, repo.events, before, "unauthorized attempts must not reach decryption repository calls")
		})
	}
}

func TestAuditDisclosureCommitsStartedBeforeDecryptAndCompletionBeforeReturn(t *testing.T) {
	repo := &auditManagementRepoStub{}
	svc, codec := newReadyAuditManagement(t, repo)
	repo.material = syntheticDisclosureMaterial(t, codec)
	result, err := svc.Disclose(context.Background(), AuditDisclosureInput{
		InteractionID: repo.material.Metadata.ID,
		Actor:         AuditDisclosureActor{UserID: 7, SessionVersion: 3, SessionExpiresAt: time.Now().Add(time.Hour), Role: RoleSuperAdmin, AuthMethod: "jwt"},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"started", "loaded", "completed:succeeded"}, repo.events)
	require.Len(t, result.Parts, 1)
	require.Contains(t, result.Parts[0].Content, "auditManagement-synthetic")

	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	for _, forbidden := range []string{"key_version", "nonce", "ciphertext", "auth_tag", "api_key_id", "api_key_fingerprint"} {
		require.NotContains(t, string(encoded), forbidden)
	}
}

func TestAuditDisclosureCompletesGovernanceAfterClientCancellation(t *testing.T) {
	repo := &auditManagementRepoStub{}
	svc, codec := newReadyAuditManagement(t, repo)
	repo.material = syntheticDisclosureMaterial(t, codec)
	ctx, cancel := context.WithCancel(context.Background())
	repo.cancelOnLoad = cancel

	result, err := svc.Disclose(ctx, AuditDisclosureInput{
		InteractionID: repo.material.Metadata.ID,
		Actor:         AuditDisclosureActor{UserID: 7, SessionVersion: 3, SessionExpiresAt: time.Now().Add(time.Hour), Role: RoleSuperAdmin, AuthMethod: "jwt"},
	})
	require.NoError(t, err)
	require.NoError(t, repo.completionContextErr)
	require.Equal(t, []string{"started", "loaded", "completed:succeeded"}, repo.events)
	require.Len(t, result.Parts, 1)
}

func TestAuditDisclosureFailsClosedOnGovernanceOrDecryptionFailure(t *testing.T) {
	t.Run("completion event failure", func(t *testing.T) {
		repo := &auditManagementRepoStub{completeErr: errors.New("synthetic governance failure")}
		svc, codec := newReadyAuditManagement(t, repo)
		repo.material = syntheticDisclosureMaterial(t, codec)
		result, err := svc.Disclose(context.Background(), AuditDisclosureInput{
			InteractionID: repo.material.Metadata.ID,
			Actor:         AuditDisclosureActor{UserID: 7, SessionVersion: 3, SessionExpiresAt: time.Now().Add(time.Hour), Role: RoleSuperAdmin, AuthMethod: "jwt"},
		})
		require.ErrorIs(t, err, ErrAuditGovernanceUnavailable)
		require.Empty(t, result.Parts)
		require.Equal(t, []string{"started", "loaded", "completed:succeeded"}, repo.events)
	})

	t.Run("authenticated ciphertext failure", func(t *testing.T) {
		repo := &auditManagementRepoStub{}
		svc, codec := newReadyAuditManagement(t, repo)
		repo.material = syntheticDisclosureMaterial(t, codec)
		repo.material.Parts[0].Encrypted.AuthTag[0] ^= 0xff
		result, err := svc.Disclose(context.Background(), AuditDisclosureInput{
			InteractionID: repo.material.Metadata.ID,
			Actor:         AuditDisclosureActor{UserID: 7, SessionVersion: 3, SessionExpiresAt: time.Now().Add(time.Hour), Role: RoleSuperAdmin, AuthMethod: "jwt"},
		})
		require.ErrorIs(t, err, ErrAuditContentUnavailable)
		require.Empty(t, result.Parts)
		require.Equal(t, []string{"started", "loaded", "completed:failed"}, repo.events)
	})
}

func TestAuditRetentionUsesExactCutoffAndBoundedBatch(t *testing.T) {
	cutoff := time.Date(2026, 8, 6, 1, 2, 3, 0, time.UTC)
	repo := &auditManagementRepoStub{retention: AuditRetentionResult{Candidates: 3, Purged: 2, Failed: 1}}
	svc := NewAuditManagementService(repo, nil)
	result, err := svc.RunRetention(context.Background(), cutoff)
	require.NoError(t, err)
	require.Equal(t, repo.retention, result)
	require.Equal(t, cutoff, repo.cutoff)
	require.Equal(t, auditRetentionBatchSize, repo.batchSize)
}
