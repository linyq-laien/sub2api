package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGeminiAICodeRequestAndTierRetry(t *testing.T) {
	a := &Account{Platform: PlatformGemini, Type: AccountTypeOAuth, Credentials: map[string]any{"client_id": geminicli.AICodeOAuthClientID, "project_id": "stale-project"}}
	require.True(t, a.IsGeminiAICode())
	req, err := buildGeminiAICodeRequest(context.Background(), a, "token", "gemini-3.5-flash-lite", "streamGenerateContent", []byte(`{"model":"remove","contents":[{"parts":[{"inlineData":{"mimeType":"video/mp4","data":"AA=="}}]}]}`))
	require.NoError(t, err)
	calls := 0
	resp, err := doGeminiAICodeRequest(req, func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, "https://aicode.googleapis.com/v1alpha:generateContent", r.URL.String())
		require.Equal(t, "Bearer token", r.Header.Get("Authorization"))
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.NotContains(t, body, "model")
		require.NotContains(t, body, "project")
		require.NotContains(t, body, "request")
		require.Contains(t, body, "contents")
		if calls == 1 {
			return &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"USER_TIER_NOT_ENTITLED","metadata":{"resolved_tier":"free-tier"}}]}}`))}, nil
		}
		entitlement, ok := body["entitlement"].(map[string]any)
		require.True(t, ok)
		require.Equal(t, "free-tier", entitlement["userTier"])
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{\n\"candidates\": []\n}"))}, nil
	})
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 2, calls)
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, "data: {\"candidates\":[]}\n\n", string(raw))
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	_, err = buildGeminiAICodeRequest(context.Background(), a, "token", "model", "countTokens", []byte(`{}`))
	require.Error(t, err)
}

func TestGeminiAICodeLicenseErrorNotRetried(t *testing.T) {
	a := &Account{Credentials: map[string]any{}}
	req, err := buildGeminiAICodeRequest(context.Background(), a, "token", "model", "generateContent", []byte(`{}`))
	require.NoError(t, err)
	calls := 0
	resp, err := doGeminiAICodeRequest(req, func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader(`{"error":{"details":[{"reason":"SUBSCRIPTION_REQUIRED"}]}}`))}, nil
	})
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, 1, calls)
	require.Equal(t, 403, resp.StatusCode)
}

func TestGeminiAICodeChatCompletions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		t.Run(map[bool]string{false: "json", true: "stream"}[stream], func(t *testing.T) {
			stub := &geminiCompatHTTPUpstreamStub{response: &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"role":"model","parts":[{"text":"OK"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":1}}`))}}
			svc := &GeminiMessagesCompatService{tokenProvider: &GeminiTokenProvider{}, httpUpstream: stub, cfg: &config.Config{}}
			account := &Account{Platform: PlatformGemini, Type: AccountTypeOAuth, Credentials: map[string]any{"oauth_type": "aicode", "project_id": "stale", "access_token": "test"}}
			body, _ := json.Marshal(map[string]any{"model": "gemini-3.5-flash-lite", "messages": []map[string]string{{"role": "user", "content": "hi"}}, "stream": stream})
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
			result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body)
			require.NoError(t, err)
			require.Equal(t, 7, result.Usage.InputTokens)
			require.Contains(t, rec.Body.String(), "OK")
			require.Equal(t, "aicode.googleapis.com", stub.lastReq.URL.Host)
		})
	}
}

func TestGeminiAICodeNativeAndMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"native", "messages"} {
		for _, stream := range []bool{false, true} {
			t.Run(endpoint+map[bool]string{false: "JSON", true: "SSE"}[stream], func(t *testing.T) {
				stub := &geminiCompatHTTPUpstreamStub{response: &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"role":"model","parts":[{"text":"OK"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":1}}`))}}
				svc := &GeminiMessagesCompatService{tokenProvider: &GeminiTokenProvider{}, httpUpstream: stub, cfg: &config.Config{}}
				account := &Account{ID: 99, Platform: PlatformGemini, Type: AccountTypeOAuth, Credentials: map[string]any{"oauth_type": "aicode", "access_token": "test", "project_id": "stale"}}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest("POST", "/", nil)
				if endpoint == "native" {
					action := "generateContent"
					if stream {
						action = "streamGenerateContent"
					}
					_, err := svc.ForwardNative(context.Background(), c, account, "gemini-3.5-flash-lite", action, stream, []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
					require.NoError(t, err)
				} else {
					body, _ := json.Marshal(map[string]any{"model": "gemini-3.5-flash-lite", "max_tokens": 128, "stream": stream, "messages": []map[string]string{{"role": "user", "content": "hi"}}})
					_, err := svc.Forward(context.Background(), c, account, body)
					require.NoError(t, err)
				}
				require.Contains(t, rec.Body.String(), "OK")
				require.Equal(t, "aicode.googleapis.com", stub.lastReq.URL.Host)
			})
		}
	}
}

func TestGeminiAICodeAccountTestAndCacheIdentity(t *testing.T) {
	a := &Account{ID: 42, Platform: PlatformGemini, Type: AccountTypeOAuth, Credentials: map[string]any{"client_id": geminicli.AICodeOAuthClientID, "oauth_type": "code_assist", "access_token": "test", "project_id": "shared"}}
	require.Equal(t, "aicode", a.GeminiOAuthType())
	require.False(t, a.IsGeminiCodeAssist())
	require.Equal(t, "gemini:aicode:account:42", GeminiTokenCacheKey(a))
	svc := &AccountTestService{geminiTokenProvider: &GeminiTokenProvider{}}
	req, err := svc.buildGeminiOAuthRequest(context.Background(), a, geminiDefaultTestModel(a), []byte(`{"contents":[]}`))
	require.NoError(t, err)
	require.Equal(t, "aicode.googleapis.com", req.URL.Host)
	require.Equal(t, "gemini-3.5-flash-lite", geminiDefaultTestModel(a))
}

func TestGeminiAICodeModelDiscovery(t *testing.T) {
	stub := &geminiCompatHTTPUpstreamStub{response: &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"availableExperiences":[{"name":"gemini-3.5-flash-lite"}]}`))}}
	svc := &GeminiMessagesCompatService{tokenProvider: &GeminiTokenProvider{}, httpUpstream: stub, cfg: &config.Config{}}
	a := &Account{Platform: PlatformGemini, Type: AccountTypeOAuth, Credentials: map[string]any{"oauth_type": "aicode", "access_token": "test"}}
	result, err := svc.ForwardListModels(context.Background(), a)
	require.NoError(t, err)
	require.Equal(t, "https://aicode.googleapis.com/v1alpha:fetchConfig", stub.lastReq.URL.String())
	require.Contains(t, string(result.Body), "models/gemini-3.5-flash-lite")
}

// The admin "sync upstream models" button must list AI Code experiences; AI Code
// tokens are rejected by /v1beta/models, and a stale project_id must not divert
// the account into the Code Assist guard.
func TestGeminiAICodeUpstreamModelSync(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(
		`{"availableExperiences":[{"name":"gemini-3.5-flash-lite"},{"name":"models/exp-b"},{"name":" "},{"name":"gemini-3.5-flash-lite"}]}`))}}
	svc := &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig(), geminiTokenProvider: &GeminiTokenProvider{}}
	a := &Account{ID: 7, Platform: PlatformGemini, Type: AccountTypeOAuth, Credentials: map[string]any{"client_id": geminicli.AICodeOAuthClientID, "access_token": "tok", "project_id": "stale"}}

	models, err := svc.FetchUpstreamSupportedModels(context.Background(), a)
	require.NoError(t, err)
	require.Equal(t, []string{"exp-b", "gemini-3.5-flash-lite"}, models)
	require.Equal(t, "https://aicode.googleapis.com/v1alpha:fetchConfig", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer tok", upstream.lastReq.Header.Get("Authorization"))

	upstream.resp = &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"error":{"code":403}}`))}
	_, err = svc.FetchUpstreamSupportedModels(context.Background(), a)
	var syncErr *UpstreamModelSyncError
	require.ErrorAs(t, err, &syncErr)
	require.Equal(t, http.StatusForbidden, syncErr.StatusCode)
}
