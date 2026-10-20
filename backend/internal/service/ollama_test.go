//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func ollamaTestAccount() *Account {
	return &Account{
		ID:          1,
		Name:        "ollama",
		Platform:    PlatformOllama,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "ollama_test_key", "api_protocol": APIProtocolAdaptive},
	}
}

func TestOllamaProfileChatCompletionsOnly(t *testing.T) {
	account := ollamaTestAccount()
	require.True(t, account.IsMultiProtocolAPIKey())
	require.False(t, account.routesByModel())
	require.Equal(t, DefaultOllamaBaseURL, account.GetOpenAIBaseURL())
	require.False(t, account.providerSupportsProtocol(APIProtocolResponses))
	require.Equal(t, DefaultOllamaTestModel, account.providerDefaultTestModel())
	for _, inbound := range []string{APIProtocolChatCompletions, APIProtocolResponses, APIProtocolAnthropic} {
		require.Equal(t, APIProtocolChatCompletions, resolveUpstreamProtocol(account, inbound, "deepseek-v4.1-flash:1017-cloud", nil), inbound)
	}
}

// Codex Responses Lite 把运行时工具放进 input[].additional_tools；Ollama 的 Responses
// 端点拒绝该 input 项（400 unknown input item type），故必须降级为 Chat Completions，
// 且工具要出现在 Chat 请求的 tools 里。
func TestOllamaResponsesAdditionalToolsLoweredToChatCompletions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4.1-flash:1017-cloud","stream":false,"input":[` +
		`{"type":"additional_tools","tools":[{"type":"function","name":"shell","description":"run","parameters":{"type":"object","properties":{}}}]},` +
		`{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"x","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`)),
	}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

	_, err := svc.Forward(context.Background(), c, ollamaTestAccount(), body)

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "https://ollama.com/v1/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer ollama_test_key", upstream.lastReq.Header.Get("Authorization"))
	require.NotContains(t, string(upstream.lastBody), "additional_tools")
	require.Equal(t, "shell", gjson.GetBytes(upstream.lastBody, "tools.0.function.name").String())
	require.Equal(t, "hi", gjson.GetBytes(upstream.lastBody, "messages.0.content").String())
}
