package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// With a single Grok account, failover after an xAI request-shape 422 has no
// other account to try. The client must see xAI's status and reason, not the
// generic 502 "Upstream request failed".
func TestOpenAIFailoverExhausted_GrokRejectedRequestKeepsUpstreamStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(nil))

	(&OpenAIGatewayHandler{}).handleFailoverExhausted(c, &service.UpstreamFailoverError{
		StatusCode:       http.StatusUnprocessableEntity,
		ResponseBody:     []byte(`{"code":"invalid_compaction","error":"could not decode the compaction blob"}`),
		Reason:           service.GrokRequestRejectedReason,
		ClientStatusCode: http.StatusUnprocessableEntity,
		ClientMessage:    "could not decode the compaction blob",
	}, false)

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
	var envelope map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope))
	errBody, ok := envelope["error"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "invalid_request_error", errBody["type"])
	require.Equal(t, "could not decode the compaction blob", errBody["message"])
}
