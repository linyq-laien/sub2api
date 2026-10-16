//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Codex web.run calls /alpha/search, which xAI does not have. Grok accounts
// serve it as a Responses turn with the hosted web_search tool; the fixture is
// a real cli-chat-proxy grok-4.7 stream (trimmed, ciphertext removed).
func TestForwardAlphaSearchGrokUsesResponsesWebSearch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sse, err := os.ReadFile("testdata/grok_alpha_search_sse.txt")
	require.NoError(t, err)

	body := []byte(`{"id":"search-session","model":"gpt-6.1-sol","commands":{"search_query":[{"q":"AWS Bedrock what is it","domains":["aws.amazon.com"]}]},"settings":{"external_web_access":true}}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search", bytes.NewReader(body))
	c.Set("api_key", &APIKey{ID: 4701})

	account := grokChatBridgeTestAccount(4701)
	account.Credentials["model_mapping"] = map[string]any{"gpt-6.1-sol": "grok-4.7"}
	repo := &grokQuotaAccountRepo{mockAccountRepoForPlatform: &mockAccountRepoForPlatform{accountsByID: map[int64]*Account{account.ID: account}}}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(bytes.NewReader(sse)),
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream, grokTokenProvider: NewGrokTokenProvider(repo, nil), accountRepo: repo}

	result, err := svc.ForwardAlphaSearch(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.WebSearchCalls)
	require.Equal(t, "grok-4.7", result.UpstreamModel)

	require.Equal(t, xai.DefaultCLIBaseURL+"/responses", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer access-token", upstream.lastReq.Header.Get("Authorization"))
	require.Equal(t, "grok-4.7", gjson.GetBytes(upstream.lastBody, "model").String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.Equal(t, "web_search", gjson.GetBytes(upstream.lastBody, "tools.0.type").String())
	require.Contains(t, gjson.GetBytes(upstream.lastBody, "input.0.content.0.text").String(), "AWS Bedrock what is it")

	require.Equal(t, http.StatusOK, recorder.Code)
	out := recorder.Body.Bytes()
	require.Contains(t, gjson.GetBytes(out, "output").String(), "Amazon Bedrock")
	require.Equal(t, int64(3), gjson.GetBytes(out, "results.#").Int())
	require.Equal(t, "text_result", gjson.GetBytes(out, "results.0.type").String())
	require.True(t, strings.HasPrefix(gjson.GetBytes(out, "results.0.url").String(), "https://docs.aws.amazon.com/bedrock/"))
}

func TestForwardAlphaSearchGrokRateLimitFailsOver(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"id":"s","model":"gpt-6.1-sol","commands":{"search_query":[{"q":"x"}]}}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/alpha/search", bytes.NewReader(body))

	account := &Account{
		ID: 4702, Name: "grok-api-key", Platform: PlatformGrok, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "token", "base_url": "https://api.x.ai/v1"},
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"code":"rate_limited","error":"Too many requests"}`)),
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}

	result, err := svc.ForwardAlphaSearch(context.Background(), c, account, body)
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.Zero(t, recorder.Body.Len())
}

func TestGrokAccountSupportsAlphaSearchCapability(t *testing.T) {
	account := &Account{Platform: PlatformGrok, Type: AccountTypeOAuth}
	require.True(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityAlphaSearch))
	require.False(t, account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilitySeedance))
}
