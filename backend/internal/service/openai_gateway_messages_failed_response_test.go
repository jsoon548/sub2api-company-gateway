//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func buildResponsesFailedSSEStream(errType, errorMessage string) string {
	failed := fmt.Sprintf(`{"type":"response.failed","response":{"id":"resp_err","object":"response","status":"failed","error":{"type":"%s","message":"%s"},"output":[],"usage":{"input_tokens":10,"output_tokens":0,"total_tokens":10}}}`, errType, errorMessage)
	return fmt.Sprintf("data: %s\n\n", failed)
}

func TestForwardAsAnthropic_BufferedSuccessOverridesUpstreamSSEContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	ssePayload := "data: " + `{"type":"response.completed","response":{"id":"resp_ok","object":"response","model":"gpt-5.4","status":"completed","output":[{"id":"msg_ok","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}` + "\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(ssePayload)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	result, err := svc.ForwardAsAnthropic(context.Background(), c, rawChatCompletionsTestAccount(), body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "application/json; charset=utf-8", rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{"id":"resp_ok","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"gpt-5.4","stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2,"cache_creation_input_tokens":0,"cache_read_input_tokens":0}}`, rec.Body.String())
}

func TestForwardAsAnthropic_BufferedResponseFailed_ReturnsError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	ssePayload := buildResponsesFailedSSEStream("invalid_request_error", "Content policy violation")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(ssePayload)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	account := rawChatCompletionsTestAccount()
	_, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")

	require.Error(t, err, "non-cyber response.failed must return an error, not swallow as 200")
	require.Contains(t, err.Error(), "upstream response failed")
	require.Equal(t, http.StatusBadGateway, rec.Code, "should write 502 for non-failover failed response")
}

func TestForwardAsAnthropic_StreamingResponseFailed_ReturnsError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	ssePayload := buildResponsesFailedSSEStream("invalid_request_error", "Content policy violation")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(ssePayload)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	account := rawChatCompletionsTestAccount()
	_, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")

	require.Error(t, err, "streaming response.failed must return an error")
	require.Contains(t, err.Error(), "upstream response failed")
}

func TestForwardAsAnthropic_BufferedResponseFailed_Failover(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	ssePayload := buildResponsesFailedSSEStream("rate_limit_error", "Rate limit reached")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(ssePayload)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	account := rawChatCompletionsTestAccount()
	_, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")

	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr), "rate_limit_error should trigger UpstreamFailoverError for failover, got: %T: %v", err, err)
}
