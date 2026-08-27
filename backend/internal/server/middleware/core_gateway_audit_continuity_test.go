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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

var auditContinuityFixedGatewayID = uuid.MustParse("55555555-2222-4333-8444-555555555555")

type auditContinuityRepoStub struct {
	mu               sync.Mutex
	events           []string
	interaction      service.AuditInteractionRecord
	requestPart      service.AuditContentPartRecord
	responseCommits  []service.AuditResponsePartCommit
	writeResults     []service.AuditResponseWriteResult
	finalizations    []service.AuditInteractionFinalization
	commitErrAt      int
	writeResultErrAt int
	finalizeErr      error
	admitErr         error
	commitWaitForCtx bool
	commitIgnoreCtx  <-chan struct{}
}

func (s *auditContinuityRepoStub) event(value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, value)
}

func (s *auditContinuityRepoStub) CheckFoundation(context.Context) error { return nil }
func (s *auditContinuityRepoStub) CreateInteraction(context.Context, service.AuditInteractionRecord) error {
	return nil
}
func (s *auditContinuityRepoStub) AppendEncryptedPart(context.Context, service.AuditContentPartRecord) error {
	return nil
}
func (s *auditContinuityRepoStub) AdmitRequest(_ context.Context, interaction service.AuditInteractionRecord, part service.AuditContentPartRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, "admit")
	s.interaction, s.requestPart = interaction, part
	return s.admitErr
}
func (s *auditContinuityRepoStub) CommitResponsePart(ctx context.Context, commit service.AuditResponsePartCommit) error {
	if s.commitIgnoreCtx != nil {
		s.event("commit:ignoring-context")
		<-s.commitIgnoreCtx
		return nil
	}
	if s.commitWaitForCtx {
		s.event("commit:blocked")
		<-ctx.Done()
		return ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	call := len(s.responseCommits) + 1
	s.events = append(s.events, "commit:"+strconv.Itoa(call))
	if s.commitErrAt == call {
		return errors.New("synthetic response commit failure")
	}
	s.responseCommits = append(s.responseCommits, commit)
	return nil
}
func (s *auditContinuityRepoStub) SetResponseWriteResult(_ context.Context, result service.AuditResponseWriteResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	call := len(s.writeResults) + 1
	s.events = append(s.events, "write-result:"+strconv.Itoa(call))
	if s.writeResultErrAt == call {
		return errors.New("synthetic write-result failure")
	}
	s.writeResults = append(s.writeResults, result)
	return nil
}
func (s *auditContinuityRepoStub) FinalizeInteraction(_ context.Context, final service.AuditInteractionFinalization) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, "finalize")
	if s.finalizeErr != nil {
		return s.finalizeErr
	}
	s.finalizations = append(s.finalizations, final)
	return nil
}
func (s *auditContinuityRepoStub) CompareAndSwapRequestOutcome(context.Context, service.AuditStateCAS) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.finalizations) == 0, nil
}
func (s *auditContinuityRepoStub) CompareAndSwapContentState(context.Context, service.AuditStateCAS) (bool, error) {
	return true, nil
}
func (s *auditContinuityRepoStub) ReconcileStale(context.Context, time.Time) (int64, error) {
	return 0, nil
}

type orderedHTTPWriter struct {
	header     http.Header
	body       bytes.Buffer
	status     int
	events     *[]string
	mu         *sync.Mutex
	writeErrAt int
	shortAt    int
	writes     int
}

func newOrderedHTTPWriter(events *[]string, mu *sync.Mutex) *orderedHTTPWriter {
	return &orderedHTTPWriter{header: make(http.Header), status: http.StatusOK, events: events, mu: mu}
}
func (w *orderedHTTPWriter) Header() http.Header { return w.header }
func (w *orderedHTTPWriter) WriteHeader(status int) {
	if w.status == http.StatusOK {
		w.status = status
	}
}
func (w *orderedHTTPWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	w.writes++
	*w.events = append(*w.events, "downstream-write:"+strconv.Itoa(w.writes))
	call := w.writes
	w.mu.Unlock()
	if w.writeErrAt == call {
		return 0, errors.New("synthetic downstream write failure")
	}
	if w.shortAt == call && len(data) > 0 {
		return w.body.Write(data[:len(data)-1])
	}
	return w.body.Write(data)
}
func (w *orderedHTTPWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	*w.events = append(*w.events, "downstream-flush")
}

func newReadyContinuityService(t *testing.T, repo *auditContinuityRepoStub) *service.AuditFoundationService {
	t.Helper()
	const keyName = "AUDIT_CONTINUITY_CONTINUITY_AUDIT_KEY"
	t.Setenv(keyName, base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x75}, 32)))
	svc := service.NewAuditFoundationService(repo, config.AuditConfig{
		Mode: service.AuditModeRequired, ContentKeyRef: "env:" + keyName,
		ContentKeyVersion: "v1", ReconcileIntervalSeconds: 3600,
	})
	svc.Start()
	t.Cleanup(svc.Stop)
	return svc
}

func fixedAuditContinuityRequestID(c *gin.Context) {
	c.Set(gatewayRequestIDKey, auditContinuityFixedGatewayID)
	c.Request.Header.Set(GatewayRequestIDHeader, auditContinuityFixedGatewayID.String())
	c.Header(GatewayRequestIDHeader, auditContinuityFixedGatewayID.String())
	c.Next()
}

func auditContinuityRouter(t *testing.T, repo *auditContinuityRepoStub, profile string, handler gin.HandlerFunc) (*gin.Engine, *service.AuditFoundationService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc := newReadyContinuityService(t, repo)
	router := gin.New()
	path := "/v1/responses"
	if profile == service.ProtocolProfileAnthropicMessagesV1 {
		path = "/v1/messages"
	}
	router.POST(path, fixedAuditContinuityRequestID, setAuditAdmissionAPIKey,
		CoreGatewayAuditAdmission(svc, profile), CoreGatewayAuditContinuity(svc, profile), handler)
	return router, svc
}

func decryptResponseParts(t *testing.T, svc *service.AuditFoundationService, repo *auditContinuityRepoStub) ([]auditResponseEnvelope, []byte) {
	t.Helper()
	codec, ok := svc.Codec()
	require.True(t, ok)
	envelopes := make([]auditResponseEnvelope, 0, len(repo.responseCommits))
	var body []byte
	for index, commit := range repo.responseCommits {
		plaintext, err := codec.Decrypt(service.AuditPartAAD{
			InteractionID: repo.interaction.ID, GatewayRequestID: repo.interaction.GatewayRequestID,
			Direction: "response", Sequence: index, AdmittedAt: repo.interaction.AdmittedAt, KeyVersion: "v1",
		}, commit.Part.Encrypted)
		require.NoError(t, err)
		var envelope auditResponseEnvelope
		require.NoError(t, json.Unmarshal(plaintext, &envelope))
		envelopes = append(envelopes, envelope)
		body = append(body, envelope.Body...)
	}
	return envelopes, body
}

func TestAuditContinuityUnaryGoldenCapturesFinalGatewayBytes(t *testing.T) {
	tests := []struct {
		name        string
		profile     string
		status      int
		contentType string
		parts       []string
	}{
		{name: "openai tool call", profile: service.ProtocolProfileOpenAIResponsesV1, status: http.StatusOK, contentType: "application/json", parts: []string{`{"id":"resp_auditContinuity","output":[`, `{"type":"function_call","name":"weather","arguments":"{\"city\":\"上海\"}"}]}`}},
		{name: "anthropic tool use", profile: service.ProtocolProfileAnthropicMessagesV1, status: http.StatusOK, contentType: "application/json", parts: []string{`{"id":"msg_auditContinuity","content":[`, `{"type":"tool_use","name":"weather","input":{"city":"上海"}}]}`}},
		{name: "anthropic protocol error", profile: service.ProtocolProfileAnthropicMessagesV1, status: http.StatusBadRequest, contentType: "application/json", parts: []string{`{"type":"error","error":{"type":"invalid_request_error","message":"synthetic malformed tool"}}`}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := &auditContinuityRepoStub{}
			router, svc := auditContinuityRouter(t, repo, tc.profile, func(c *gin.Context) {
				c.Header("Content-Type", tc.contentType)
				c.Header("Set-Cookie", "credential=must-not-be-captured")
				c.Header("Authorization", "Bearer must-not-be-captured")
				c.Header("X-Api-Key", "must-not-be-captured")
				c.Header("X-Synthetic-URL", "http://127.0.0.1:59535/never-fetch")
				c.Status(tc.status)
				for _, part := range tc.parts {
					_, _ = c.Writer.WriteString(part)
				}
			})
			path := "/v1/responses"
			if tc.profile == service.ProtocolProfileAnthropicMessagesV1 {
				path = "/v1/messages"
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"synthetic-model"}`)))
			expectedBody := []byte(strings.Join(tc.parts, ""))
			require.Equal(t, tc.status, recorder.Code)
			require.Equal(t, expectedBody, recorder.Body.Bytes())
			require.Equal(t, auditContinuityFixedGatewayID.String(), recorder.Header().Get(GatewayRequestIDHeader))
			require.Len(t, repo.responseCommits, 1, "unary must commit one complete response part")
			envelopes, decryptedBody := decryptResponseParts(t, svc, repo)
			require.Equal(t, expectedBody, decryptedBody)
			require.Equal(t, tc.status, envelopes[0].Status)
			require.NotContains(t, string(repo.responseCommits[0].Part.Encrypted.Ciphertext), string(expectedBody))
			plaintext, err := json.Marshal(envelopes[0])
			require.NoError(t, err)
			require.NotContains(t, string(plaintext), "must-not-be-captured")
			require.NotContains(t, string(plaintext), "127.0.0.1:59535")
			digest := sha256.Sum256(expectedBody)
			require.Equal(t, hex.EncodeToString(digest[:]), hex.EncodeToString(repo.responseCommits[0].ResponseSHA256))
			require.Equal(t, 0, repo.responseCommits[0].ExpectedPartCount)
			require.Equal(t, "succeeded", repo.writeResults[0].Result)
			require.Equal(t, service.AuditRequestCompleted, repo.finalizations[0].RequestOutcome)
			require.Equal(t, service.AuditContentComplete, repo.finalizations[0].ContentState)
		})
	}
}

func TestAuditContinuityPreservesHeaderAndRepeatedWriteHeaderSemantics(t *testing.T) {
	repo := &auditContinuityRepoStub{}
	router, svc := auditContinuityRouter(t, repo, service.ProtocolProfileOpenAIResponsesV1, func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.Header("Cache-Control", "before-first-write")
		c.Writer.WriteHeader(http.StatusCreated)
		c.Writer.WriteHeader(http.StatusAccepted)
		_, _ = c.Writer.WriteString(`{"status":"frozen"}`)
		c.Header("Cache-Control", "must-be-ignored-after-first-write")
		c.Header("Set-Cookie", "must-not-be-captured")
		c.Writer.WriteHeader(http.StatusTeapot)
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"synthetic-model"}`)))
	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.Equal(t, "before-first-write", recorder.Header().Get("Cache-Control"))
	require.Empty(t, recorder.Header().Get("Set-Cookie"))
	envelopes, _ := decryptResponseParts(t, svc, repo)
	require.Equal(t, http.StatusAccepted, envelopes[0].Status)
	require.Equal(t, []auditCapturedHeader{
		{Name: "Cache-Control", Values: []string{"before-first-write"}},
		{Name: "Content-Type", Values: []string{"application/json"}},
		{Name: GatewayRequestIDHeader, Values: []string{auditContinuityFixedGatewayID.String()}},
	}, envelopes[0].Headers)
}

func TestAuditContinuityCommitsBeforeEveryDownstreamWriteAndFlush(t *testing.T) {
	repo := &auditContinuityRepoStub{}
	router, _ := auditContinuityRouter(t, repo, service.ProtocolProfileAnthropicMessagesV1, func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		for _, event := range []string{
			": ping\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"你好\"}}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"content_block\":{\"type\":\"tool_use\",\"name\":\"weather\"}}\n\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		} {
			_, _ = c.Writer.WriteString(event)
			c.Writer.Flush()
		}
	})
	writer := newOrderedHTTPWriter(&repo.events, &repo.mu)
	router.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"synthetic-model","stream":true}`)))
	require.Equal(t, service.AuditRequestCompleted, repo.finalizations[0].RequestOutcome)
	require.Len(t, repo.responseCommits, 4)
	require.Equal(t, []int{0, 1, 2, 3}, []int{
		repo.responseCommits[0].Part.Sequence, repo.responseCommits[1].Part.Sequence,
		repo.responseCommits[2].Part.Sequence, repo.responseCommits[3].Part.Sequence,
	})
	for i := 1; i <= 4; i++ {
		commitIndex := indexOf(repo.events, "commit:"+strconv.Itoa(i))
		writeIndex := indexOf(repo.events, "downstream-write:"+strconv.Itoa(i))
		require.Greater(t, writeIndex, commitIndex, "response part commit must precede downstream Write")
	}
	for i, event := range repo.events {
		if event == "downstream-flush" {
			priorCommit := false
			for _, earlier := range repo.events[:i] {
				if strings.HasPrefix(earlier, "commit:") {
					priorCommit = true
				}
			}
			require.True(t, priorCommit, "Flush must never precede the first committed response batch")
		}
	}
}

func TestAuditContinuitySSESplitUTF8MultiEventAndOversizedEvent(t *testing.T) {
	repo := &auditContinuityRepoStub{}
	large := strings.Repeat("x", auditSSEBatchBytes+257)
	pieces := []string{
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"你",
		"好\"}\n\n: ping\n\nevent: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\"}}\n\n",
		"event: synthetic.large\ndata: " + large + "\n\n",
		"event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n",
	}
	router, svc := auditContinuityRouter(t, repo, service.ProtocolProfileOpenAIResponsesV1, func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		_, _ = c.Writer.WriteString(pieces[0])
		_, _ = c.Writer.WriteString(pieces[1])
		c.Writer.Flush()
		_, _ = c.Writer.WriteString(pieces[2])
		_, _ = c.Writer.WriteString(pieces[3])
		c.Writer.Flush()
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"synthetic-model","stream":true}`)))
	expected := []byte(strings.Join(pieces, ""))
	require.Equal(t, expected, recorder.Body.Bytes())
	_, decrypted := decryptResponseParts(t, svc, repo)
	require.Equal(t, expected, decrypted)
	require.GreaterOrEqual(t, len(repo.responseCommits), 3, "oversized event must be bounded into multiple transactions")
	for index, commit := range repo.responseCommits {
		require.Equal(t, index, commit.Part.Sequence)
		codec, ok := svc.Codec()
		require.True(t, ok)
		decoded, decryptErr := codec.Decrypt(service.AuditPartAAD{
			InteractionID: repo.interaction.ID, GatewayRequestID: repo.interaction.GatewayRequestID,
			Direction: "response", Sequence: index, AdmittedAt: repo.interaction.AdmittedAt, KeyVersion: "v1",
		}, commit.Part.Encrypted)
		require.NoError(t, decryptErr)
		var envelope auditResponseEnvelope
		require.NoError(t, json.Unmarshal(decoded, &envelope))
		require.LessOrEqual(t, len(envelope.Body), auditSSEBatchBytes, "raw SSE batch bytes stay bounded")
	}
	digest := sha256.Sum256(expected)
	require.Equal(t, digest[:], repo.responseCommits[len(repo.responseCommits)-1].ResponseSHA256)
}

func TestAuditContinuityFailureMatrix(t *testing.T) {
	t.Run("blocked database commit times out and cancels upstream", func(t *testing.T) {
		previous := auditResponseCommitTimeout
		auditResponseCommitTimeout = 30 * time.Millisecond
		t.Cleanup(func() { auditResponseCommitTimeout = previous })
		repo := &auditContinuityRepoStub{commitWaitForCtx: true}
		var canceled atomic.Int64
		router, _ := auditContinuityRouter(t, repo, service.ProtocolProfileOpenAIResponsesV1, func(c *gin.Context) {
			_, _ = c.Writer.WriteString("event: response.output_text.delta\ndata: upstream-secret\n\n")
			c.Writer.Flush()
			select {
			case <-c.Request.Context().Done():
				canceled.Add(1)
			case <-time.After(time.Second):
				t.Fatal("upstream context was not canceled after bounded audit commit")
			}
		})
		recorder := httptest.NewRecorder()
		started := time.Now()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"synthetic-model","stream":true}`)))
		require.Less(t, time.Since(started), time.Second)
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		require.NotContains(t, recorder.Body.String(), "upstream-secret")
		require.Equal(t, int64(1), canceled.Load())
	})

	t.Run("hard deadline does not depend on repository honoring context", func(t *testing.T) {
		previous := auditResponseCommitTimeout
		auditResponseCommitTimeout = 30 * time.Millisecond
		t.Cleanup(func() { auditResponseCommitTimeout = previous })
		release := make(chan struct{})
		repo := &auditContinuityRepoStub{commitIgnoreCtx: release}
		var canceled atomic.Int64
		router, _ := auditContinuityRouter(t, repo, service.ProtocolProfileOpenAIResponsesV1, func(c *gin.Context) {
			_, _ = c.Writer.WriteString("event: response.output_text.delta\ndata: must-not-leak\n\n")
			c.Writer.Flush()
			if c.Request.Context().Err() != nil {
				canceled.Add(1)
			}
		})
		recorder := httptest.NewRecorder()
		started := time.Now()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"synthetic-model","stream":true}`)))
		close(release)
		require.Less(t, time.Since(started), time.Second)
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		require.NotContains(t, recorder.Body.String(), "must-not-leak")
		require.Equal(t, int64(1), canceled.Load())
	})

	t.Run("first batch database failure leaks no upstream bytes and cancels", func(t *testing.T) {
		repo := &auditContinuityRepoStub{commitErrAt: 1}
		var canceled atomic.Int64
		router, _ := auditContinuityRouter(t, repo, service.ProtocolProfileOpenAIResponsesV1, func(c *gin.Context) {
			_, _ = c.Writer.WriteString("event: response.output_text.delta\ndata: upstream-secret\n\n")
			c.Writer.Flush()
			select {
			case <-c.Request.Context().Done():
				canceled.Add(1)
			default:
			}
		})
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"synthetic-model","stream":true}`)))
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		require.NotContains(t, recorder.Body.String(), "upstream-secret")
		require.Contains(t, recorder.Body.String(), "gateway_audit_unavailable")
		require.Equal(t, auditContinuityFixedGatewayID.String(), recorder.Header().Get(GatewayRequestIDHeader))
		require.Equal(t, int64(1), canceled.Load())
		require.Equal(t, service.AuditRequestInterrupted, repo.finalizations[0].RequestOutcome)
	})

	t.Run("midstream database failure stops after last durable batch", func(t *testing.T) {
		repo := &auditContinuityRepoStub{commitErrAt: 2}
		var canceled atomic.Int64
		first := "event: response.output_text.delta\ndata: first-durable\n\n"
		second := "event: response.output_text.delta\ndata: must-not-leak\n\n"
		router, _ := auditContinuityRouter(t, repo, service.ProtocolProfileOpenAIResponsesV1, func(c *gin.Context) {
			_, _ = c.Writer.WriteString(first)
			c.Writer.Flush()
			_, _ = c.Writer.WriteString(second)
			c.Writer.Flush()
			select {
			case <-c.Request.Context().Done():
				canceled.Add(1)
			default:
			}
			_, err := c.Writer.WriteString("later")
			require.Error(t, err)
		})
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"synthetic-model","stream":true}`)))
		require.Equal(t, first, recorder.Body.String())
		require.Equal(t, int64(1), canceled.Load())
		require.Len(t, repo.responseCommits, 1)
		require.Equal(t, service.AuditRequestInterrupted, repo.finalizations[0].RequestOutcome)
		require.Equal(t, service.AuditContentIncomplete, repo.finalizations[0].ContentState)
	})

	t.Run("completion failure does not advertise complete", func(t *testing.T) {
		repo := &auditContinuityRepoStub{finalizeErr: errors.New("synthetic completion update failure")}
		router, _ := auditContinuityRouter(t, repo, service.ProtocolProfileOpenAIResponsesV1, func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"id": "resp_completion_failure"})
		})
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"synthetic-model"}`)))
		require.Equal(t, http.StatusOK, recorder.Code)
		require.Empty(t, repo.finalizations, "failed completion CAS must leave processing/recording for reconciliation")
		require.Contains(t, repo.events, "finalize")
	})

	t.Run("downstream write error is gateway failure not client consumption claim", func(t *testing.T) {
		repo := &auditContinuityRepoStub{}
		router, _ := auditContinuityRouter(t, repo, service.ProtocolProfileAnthropicMessagesV1, func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"content": "synthetic"})
		})
		writer := newOrderedHTTPWriter(&repo.events, &repo.mu)
		writer.writeErrAt = 1
		router.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"synthetic-model"}`)))
		require.Equal(t, "failed", repo.writeResults[0].Result)
		require.Equal(t, service.AuditRequestInterrupted, repo.finalizations[0].RequestOutcome)
		require.Equal(t, "failed", repo.finalizations[0].WriteResult)
	})

	t.Run("downstream short write is recorded as failed", func(t *testing.T) {
		repo := &auditContinuityRepoStub{}
		router, _ := auditContinuityRouter(t, repo, service.ProtocolProfileOpenAIResponsesV1, func(c *gin.Context) {
			_, _ = c.Writer.WriteString(`{"id":"short-write"}`)
		})
		writer := newOrderedHTTPWriter(&repo.events, &repo.mu)
		writer.shortAt = 1
		router.ServeHTTP(writer, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"synthetic-model"}`)))
		require.Equal(t, "failed", repo.writeResults[0].Result)
		require.Equal(t, service.AuditRequestInterrupted, repo.finalizations[0].RequestOutcome)
	})

	t.Run("upstream failure before first response is captured as final native error", func(t *testing.T) {
		repo := &auditContinuityRepoStub{}
		router, _ := auditContinuityRouter(t, repo, service.ProtocolProfileAnthropicMessagesV1, func(c *gin.Context) {
			c.JSON(http.StatusBadGateway, gin.H{"type": "error", "error": gin.H{"type": "api_error", "message": "upstream unavailable"}})
		})
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"synthetic-model"}`)))
		require.Equal(t, http.StatusBadGateway, recorder.Code)
		require.Equal(t, service.AuditRequestUpstreamFailed, repo.finalizations[0].RequestOutcome)
		require.Equal(t, service.AuditContentComplete, repo.finalizations[0].ContentState)
	})

	t.Run("upstream stream failure is deterministic incomplete", func(t *testing.T) {
		repo := &auditContinuityRepoStub{}
		router, _ := auditContinuityRouter(t, repo, service.ProtocolProfileOpenAIResponsesV1, func(c *gin.Context) {
			c.Header("Content-Type", "text/event-stream")
			_, _ = c.Writer.WriteString("event: response.output_text.delta\ndata: {\"delta\":\"partial\"}\n\n")
			c.Writer.Flush()
		})
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"synthetic-model","stream":true}`)))
		require.Equal(t, service.AuditRequestUpstreamFailed, repo.finalizations[0].RequestOutcome)
		require.Equal(t, service.AuditContentIncomplete, repo.finalizations[0].ContentState)
	})
}

func TestAuditContinuityModelRejectionRemainsRejectedPreUpstream(t *testing.T) {
	repo := &auditContinuityRepoStub{}
	svc := newReadyContinuityService(t, repo)
	upstream := 0
	router := gin.New()
	router.POST("/v1/messages", fixedAuditContinuityRequestID, setAuditAdmissionAPIKey,
		CoreGatewayAuditAdmission(svc, service.ProtocolProfileAnthropicMessagesV1),
		CoreGatewayAuditContinuity(svc, service.ProtocolProfileAnthropicMessagesV1),
		CoreGatewayExplicitModelAdmission(service.NewExplicitModelResolver(&auditAdmissionCatalogStub{}), service.ProtocolProfileAnthropicMessagesV1),
		func(c *gin.Context) { upstream++ })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"not-approved"}`)))
	require.Zero(t, upstream)
	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Equal(t, auditContinuityFixedGatewayID.String(), gjsonString(recorder.Body.Bytes(), "gateway_request_id"))
	require.Equal(t, service.AuditRequestRejectedPreUpstream, repo.finalizations[0].RequestOutcome)
	require.Equal(t, service.AuditContentComplete, repo.finalizations[0].ContentState)
}

func TestAuditContinuityClientDisconnectStopsFurtherOutput(t *testing.T) {
	repo := &auditContinuityRepoStub{}
	disconnect := make(chan struct{})
	router, _ := auditContinuityRouter(t, repo, service.ProtocolProfileOpenAIResponsesV1, func(c *gin.Context) {
		_, _ = c.Writer.WriteString("event: response.output_text.delta\ndata: first\n\n")
		c.Writer.Flush()
		close(disconnect)
		<-c.Request.Context().Done()
		_, _ = c.Writer.WriteString("event: response.output_text.delta\ndata: after-disconnect\n\n")
		c.Writer.Flush()
	})
	base := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"synthetic-model","stream":true}`))
	ctx, cancel := context.WithCancel(base.Context())
	req := base.WithContext(ctx)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		router.ServeHTTP(recorder, req)
	}()
	<-disconnect
	cancel()
	<-done
	require.Contains(t, recorder.Body.String(), "data: first")
	require.NotContains(t, recorder.Body.String(), "after-disconnect")
	require.Equal(t, service.AuditRequestInterrupted, repo.finalizations[0].RequestOutcome)
	require.Equal(t, service.AuditContentIncomplete, repo.finalizations[0].ContentState)
}

func TestAuditContinuityClientDisconnectAfterTerminalRemainsComplete(t *testing.T) {
	repo := &auditContinuityRepoStub{}
	terminalFlushed := make(chan struct{})
	router, _ := auditContinuityRouter(t, repo, service.ProtocolProfileOpenAIResponsesV1, func(c *gin.Context) {
		_, _ = c.Writer.WriteString("event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n")
		c.Writer.Flush()
		close(terminalFlushed)
		<-c.Request.Context().Done()
	})
	base := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"synthetic-model","stream":true}`))
	ctx, cancel := context.WithCancel(base.Context())
	req := base.WithContext(ctx)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		router.ServeHTTP(recorder, req)
	}()
	<-terminalFlushed
	cancel()
	<-done
	require.Equal(t, service.AuditRequestCompleted, repo.finalizations[0].RequestOutcome)
	require.Equal(t, service.AuditContentComplete, repo.finalizations[0].ContentState)
	require.Equal(t, "succeeded", repo.finalizations[0].WriteResult)
}

func TestAuditContinuityUnarySizeLimitFailsClosed(t *testing.T) {
	previous := auditUnaryMaxBytes
	auditUnaryMaxBytes = 32
	t.Cleanup(func() { auditUnaryMaxBytes = previous })
	repo := &auditContinuityRepoStub{}
	router, _ := auditContinuityRouter(t, repo, service.ProtocolProfileOpenAIResponsesV1, func(c *gin.Context) {
		_, err := c.Writer.Write(bytes.Repeat([]byte("x"), 33))
		require.Error(t, err)
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"synthetic-model"}`)))
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	require.NotContains(t, recorder.Body.String(), strings.Repeat("x", 33))
	require.Empty(t, repo.responseCommits)
}

func TestAuditContinuityTimeBatchAndConcurrentKeepaliveAreRaceSafe(t *testing.T) {
	repo := &auditContinuityRepoStub{}
	router, _ := auditContinuityRouter(t, repo, service.ProtocolProfileOpenAIResponsesV1, func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, _ = c.Writer.WriteString(": ping\n\n")
			c.Writer.Flush()
		}()
		go func() {
			defer wg.Done()
			_, _ = c.Writer.WriteString("event: response.completed\ndata: {\"type\":\"response.completed\"}\n\n")
		}()
		wg.Wait()
		time.Sleep(auditSSEBatchInterval + 15*time.Millisecond)
	})
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"synthetic-model","stream":true}`)))
	require.Contains(t, recorder.Body.String(), ": ping")
	require.Contains(t, recorder.Body.String(), "response.completed")
	require.NotEmpty(t, repo.responseCommits)
}

func indexOf(values []string, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return -1
}
