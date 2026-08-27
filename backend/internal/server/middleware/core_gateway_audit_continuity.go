package middleware

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"hash"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	auditSSEBatchBytes      = 64 * 1024
	auditSSEBatchInterval   = 25 * time.Millisecond
	auditStateUpdateTimeout = 5 * time.Second
)

var auditUnaryMaxBytes = 64 * 1024 * 1024
var auditResponseCommitTimeout = 5 * time.Second

var auditAllowedResponseHeaders = map[string]struct{}{
	"Cache-Control":                 {},
	"Connection":                    {},
	"Content-Type":                  {},
	"OpenAI-Processing-Ms":          {},
	"OpenAI-Version":                {},
	"Request-Id":                    {},
	"Retry-After":                   {},
	"X-Accel-Buffering":             {},
	"X-Gateway-Actual-Model":        {},
	"X-Gateway-Model-Change-Reason": {},
	"X-Gateway-Model-Tier":          {},
	"X-Gateway-Route-Decision-ID":   {},
	"X-Gateway-Request-ID":          {},
	"X-Request-ID":                  {},
}

type auditResponseEnvelope struct {
	Version string                `json:"version"`
	Status  int                   `json:"status"`
	Headers []auditCapturedHeader `json:"headers"`
	Body    []byte                `json:"body"`
}

type auditResponseCommitResult struct {
	partID uuid.UUID
	err    error
}

type auditContinuityWriter struct {
	gin.ResponseWriter
	audit      *service.AuditFoundationService
	admission  service.AuditAdmissionResult
	protocol   string
	transport  string
	requestCtx context.Context
	cancel     context.CancelFunc

	mu               sync.Mutex
	status           int
	size             int
	written          bool
	started          bool
	sequence         int
	pending          bytes.Buffer
	headerSnapshot   []auditCapturedHeader
	downstreamHeader http.Header
	headerFrozen     bool
	timer            *time.Timer
	failure          error
	terminal         bool
	terminalFailed   bool
	inspectTail      []byte
	bodyHash         hash.Hash
}

func CoreGatewayAuditContinuity(audit *service.AuditFoundationService, profileVersion string) gin.HandlerFunc {
	protocol := protocolForCoreGatewayProfile(profileVersion)
	return func(c *gin.Context) {
		value, ok := c.Get(auditAdmissionResultKey)
		admission, valid := value.(service.AuditAdmissionResult)
		transportValue, hasTransport := c.Get(auditTransportKey)
		transport, transportOK := transportValue.(string)
		if !ok || !valid || admission.InteractionID == [16]byte{} || !hasTransport || !transportOK || (transport != "http" && transport != "sse") {
			writeCoreGatewayProtocolError(c, protocol, http.StatusServiceUnavailable, "gateway_audit_unavailable", "Audit service is unavailable; request was not sent upstream.")
			return
		}

		originalWriter := c.Writer
		requestCtx, cancel := context.WithCancel(c.Request.Context())
		c.Request = c.Request.WithContext(requestCtx)
		writer := &auditContinuityWriter{
			ResponseWriter: originalWriter, audit: audit, admission: admission,
			protocol: protocol, transport: transport, requestCtx: requestCtx,
			cancel: cancel, status: http.StatusOK, size: -1, bodyHash: sha256.New(),
		}
		c.Writer = writer
		c.Next()
		writer.finish(c)
		c.Writer = originalWriter
		cancel()
	}
}

func (w *auditContinuityWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if code > 0 && !w.written {
		w.status = code
	}
}

func (w *auditContinuityWriter) WriteHeaderNow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.markWrittenLocked()
}

func (w *auditContinuityWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failure != nil {
		return 0, w.failure
	}
	if err := w.requestCtx.Err(); err != nil {
		w.failLocked(err)
		return 0, err
	}
	w.markWrittenLocked()
	if w.transport == "http" {
		if w.pending.Len()+len(data) > auditUnaryMaxBytes {
			err := errors.New("audit unary response size limit exceeded")
			w.failLocked(err)
			return 0, err
		}
		_, _ = w.pending.Write(data)
		w.size += len(data)
		return len(data), nil
	}

	written := 0
	for len(data) > 0 {
		space := auditSSEBatchBytes - w.pending.Len()
		if space == 0 {
			if err := w.flushPendingLocked(false, false); err != nil {
				return written, err
			}
			space = auditSSEBatchBytes
		}
		take := len(data)
		if take > space {
			take = space
		}
		_, _ = w.pending.Write(data[:take])
		written += take
		w.size += take
		data = data[take:]
		if w.pending.Len() == auditSSEBatchBytes {
			if err := w.flushPendingLocked(false, false); err != nil {
				return written, err
			}
		}
	}
	w.armTimerLocked()
	return written, nil
}

func (w *auditContinuityWriter) WriteString(value string) (int, error) {
	return w.Write([]byte(value))
}

func (w *auditContinuityWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.markWrittenLocked()
	if w.transport != "sse" || w.pending.Len() == 0 || w.failure != nil {
		return
	}
	_ = w.flushPendingLocked(true, false)
}

func (w *auditContinuityWriter) Status() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

func (w *auditContinuityWriter) Size() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.size
}

func (w *auditContinuityWriter) Written() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.written
}

func (w *auditContinuityWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	err := errors.New("hijack is not supported by audited HTTP/SSE continuity")
	w.failLocked(err)
	return nil, nil, err
}

func (w *auditContinuityWriter) markWrittenLocked() {
	if !w.written {
		w.written = true
		w.size = 0
		w.downstreamHeader = w.ResponseWriter.Header().Clone()
	}
}

func (w *auditContinuityWriter) armTimerLocked() {
	if w.pending.Len() == 0 || w.failure != nil {
		return
	}
	if w.timer != nil {
		w.timer.Stop()
	}
	w.timer = time.AfterFunc(auditSSEBatchInterval, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.failure == nil && w.pending.Len() > 0 {
			_ = w.flushPendingLocked(true, false)
		}
	})
}

func (w *auditContinuityWriter) flushPendingLocked(flush, forceEmpty bool) error {
	if w.failure != nil {
		return w.failure
	}
	if err := w.requestCtx.Err(); err != nil {
		w.failLocked(err)
		return err
	}
	if w.pending.Len() == 0 && !forceEmpty {
		return nil
	}
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	batch := append([]byte(nil), w.pending.Bytes()...)
	w.pending.Reset()
	if len(batch) > auditSSEBatchBytes && w.transport == "sse" {
		return errors.New("audit SSE batch exceeded byte bound")
	}
	if !w.headerFrozen {
		header := w.downstreamHeader
		if header == nil {
			header = w.ResponseWriter.Header().Clone()
		}
		w.headerSnapshot = captureAuditResponseHeaders(header)
		w.headerFrozen = true
	}
	envelope, err := json.Marshal(auditResponseEnvelope{
		Version: "core-gateway-response-v1", Status: w.status,
		Headers: w.headerSnapshot, Body: batch,
	})
	if err != nil {
		w.failLocked(err)
		return err
	}
	_, _ = w.bodyHash.Write(batch)
	digest := w.bodyHash.Sum(nil)
	commitCtx, cancelCommit := context.WithTimeout(w.requestCtx, auditResponseCommitTimeout)
	commitResult := make(chan auditResponseCommitResult, 1)
	go func() {
		partID, commitErr := w.audit.CommitResponsePart(commitCtx, w.admission, w.sequence, envelope, digest, w.status)
		commitResult <- auditResponseCommitResult{partID: partID, err: commitErr}
	}()
	var partID uuid.UUID
	select {
	case result := <-commitResult:
		partID, err = result.partID, result.err
	case <-commitCtx.Done():
		err = commitCtx.Err()
	}
	cancelCommit()
	if err != nil {
		w.failLocked(err)
		return err
	}
	w.inspectTerminalLocked(batch)
	w.restoreDownstreamHeadersLocked()
	w.ResponseWriter.WriteHeader(w.status)
	n, writeErr := w.ResponseWriter.Write(batch)
	w.started = w.ResponseWriter.Written()
	if writeErr == nil && n != len(batch) {
		writeErr = io.ErrShortWrite
	}
	writeResult := "succeeded"
	if writeErr != nil {
		writeResult = "failed"
	}
	updateCtx, cancel := context.WithTimeout(context.WithoutCancel(w.requestCtx), auditStateUpdateTimeout)
	resultErr := w.audit.SetResponseWriteResult(updateCtx, service.AuditResponseWriteResult{
		InteractionID: w.admission.InteractionID, PartID: partID, Sequence: w.sequence,
		Result: writeResult, At: time.Now().UTC(),
	})
	cancel()
	w.sequence++
	if writeErr != nil {
		w.failLocked(writeErr)
		return writeErr
	}
	if resultErr != nil {
		w.failLocked(resultErr)
		return resultErr
	}
	if flush {
		w.ResponseWriter.Flush()
	}
	return nil
}

func (w *auditContinuityWriter) restoreDownstreamHeadersLocked() {
	if w.downstreamHeader == nil {
		return
	}
	header := w.ResponseWriter.Header()
	for name := range header {
		header.Del(name)
	}
	for name, values := range w.downstreamHeader {
		header[name] = append([]string(nil), values...)
	}
}

func (w *auditContinuityWriter) inspectTerminalLocked(batch []byte) {
	combined := append(append([]byte(nil), w.inspectTail...), batch...)
	text := string(combined)
	if strings.Contains(text, "event: message_stop") || strings.Contains(text, `"type":"message_stop"`) ||
		strings.Contains(text, "event: response.completed") || strings.Contains(text, `"type":"response.completed"`) ||
		strings.Contains(text, "data: [DONE]") {
		w.terminal = true
	}
	if strings.Contains(text, "event: response.failed") || strings.Contains(text, `"type":"response.failed"`) ||
		strings.Contains(text, "event: response.incomplete") || strings.Contains(text, `"type":"response.incomplete"`) {
		w.terminalFailed = true
	}
	const tailBytes = 512
	if len(combined) > tailBytes {
		combined = combined[len(combined)-tailBytes:]
	}
	w.inspectTail = append(w.inspectTail[:0], combined...)
}

func (w *auditContinuityWriter) failLocked(err error) {
	if err == nil || w.failure != nil {
		return
	}
	w.failure = err
	w.cancel()
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
}

func (w *auditContinuityWriter) finish(c *gin.Context) {
	w.mu.Lock()
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	if w.failure == nil {
		if w.transport == "http" {
			// An empty body still gets one encrypted response part carrying the
			// exact final status and safe headers.
			forceEmpty := w.pending.Len() == 0
			if forceEmpty {
				w.markWrittenLocked()
			}
			_ = w.flushPendingLocked(false, forceEmpty)
		} else if w.pending.Len() > 0 {
			_ = w.flushPendingLocked(true, false)
		}
	}
	failure := w.failure
	started := w.started
	status := w.status
	terminal := w.terminal
	terminalFailed := w.terminalFailed
	w.mu.Unlock()

	if failure != nil && !started {
		writeGatewayAuditUnavailableDirect(w.ResponseWriter, w.protocol, w.admission.GatewayRequestID.String())
	}

	outcome := service.AuditRequestCompleted
	contentState := service.AuditContentComplete
	writeResult := "succeeded"
	var summary *string
	// Responses clients may close immediately after a durably audited and
	// successfully written terminal event. That post-terminal cancellation is
	// not an interrupted interaction; cancellations before a terminal remain
	// fail-closed and incomplete.
	if rejection, rejected := auditPreUpstreamModelRejectionFromContext(c); rejected {
		outcome = service.AuditRequestRejectedPreUpstream
		value := rejection.SafeSummary
		summary = &value
	} else if failure != nil || (w.requestCtx.Err() != nil && !(w.transport == "sse" && terminal && !terminalFailed)) {
		outcome = service.AuditRequestInterrupted
		contentState = service.AuditContentIncomplete
		writeResult = "failed"
		value := "audit_continuity_interrupted"
		summary = &value
	} else if w.transport == "sse" && status >= 200 && status < 400 && (!terminal || terminalFailed) {
		outcome = service.AuditRequestUpstreamFailed
		contentState = service.AuditContentIncomplete
		value := "upstream_stream_incomplete"
		summary = &value
	} else if status >= http.StatusInternalServerError || terminalFailed {
		outcome = service.AuditRequestUpstreamFailed
		value := "upstream_failure_final_response"
		summary = &value
	}

	finalCtx, cancel := context.WithTimeout(context.WithoutCancel(w.requestCtx), auditStateUpdateTimeout)
	_ = w.audit.FinalizeInteraction(finalCtx, service.AuditInteractionFinalization{
		InteractionID: w.admission.InteractionID, RequestOutcome: outcome,
		ContentState: contentState, WriteResult: writeResult,
		At: time.Now().UTC(), SafeErrorSummary: summary,
	})
	cancel()
}

func captureAuditResponseHeaders(header http.Header) []auditCapturedHeader {
	names := make([]string, 0, len(auditAllowedResponseHeaders))
	for name := range auditAllowedResponseHeaders {
		if len(header.Values(name)) > 0 {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	result := make([]auditCapturedHeader, 0, len(names))
	for _, name := range names {
		result = append(result, auditCapturedHeader{Name: name, Values: append([]string(nil), header.Values(name)...)})
	}
	return result
}

func writeGatewayAuditUnavailableDirect(writer gin.ResponseWriter, protocol, gatewayID string) {
	header := writer.Header()
	for name := range header {
		header.Del(name)
	}
	header.Set(GatewayRequestIDHeader, gatewayID)
	header.Set("Content-Type", "application/json; charset=utf-8")
	message := "Audit service is unavailable; response was not sent downstream."
	var body any
	if protocol == coreGatewayProtocolAnthropic {
		body = struct {
			Type  string `json:"type"`
			Error struct {
				Type    string `json:"type"`
				Message string `json:"message"`
				Code    string `json:"code"`
			} `json:"error"`
			GatewayRequestID string `json:"gateway_request_id"`
		}{Type: "error", Error: struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Code    string `json:"code"`
		}{Type: "api_error", Message: message, Code: "gateway_audit_unavailable"}, GatewayRequestID: gatewayID}
	} else {
		body = struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
			GatewayRequestID string `json:"gateway_request_id"`
		}{Error: struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		}{Message: message, Type: "server_error", Code: "gateway_audit_unavailable"}, GatewayRequestID: gatewayID}
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return
	}
	writer.WriteHeader(http.StatusServiceUnavailable)
	_, _ = writer.Write(payload)
}
