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
)

func miMoTestAccount(mode string) *Account {
	a := &Account{ID: 1, Platform: PlatformMiMo, Type: AccountTypeAPIKey, Credentials: map[string]any{"account_mode": mode, "api_protocol": APIProtocolAdaptive}}
	switch mode {
	case MiMoModeDesktop:
		a.Credentials["mimo_session_cookie"] = "serviceToken=desktop-test; userId=123"
	case MiMoModeTokenPlan:
		a.Credentials["api_key"] = "tp-subscription-test"
	default:
		a.Credentials["api_key"] = "sk-metered-test"
	}
	return a
}

func TestMiMoCredentialIsolation(t *testing.T) {
	cases := []struct {
		name, mode, field string
		value             any
	}{
		{"subscription key in payg", AccountModePayG, "api_key", "tp-wrong"},
		{"payg key in subscription", MiMoModeTokenPlan, "api_key", "sk-wrong"},
		{"key in desktop", MiMoModeDesktop, "api_key", "sk-wrong"},
		{"cookie in payg", AccountModePayG, "mimo_session_cookie", "session=wrong"},
		{"missing desktop cookie", MiMoModeDesktop, "mimo_session_cookie", ""},
		{"header injection", MiMoModeDesktop, "mimo_session_cookie", "session=foo\r\nX-Injected: yes"},
		{"unknown mode", AccountModePayG, "account_mode", "token_play"},
		{"payg endpoint in subscription", MiMoModeTokenPlan, "base_url", "https://api.xiaomimimo.com/v1"},
		{"subscription endpoint in desktop", MiMoModeDesktop, "base_url", "https://token-plan-cn.xiaomimimo.com/v1"},
		{"desktop anthropic unsupported", MiMoModeDesktop, "api_protocol", APIProtocolAnthropic},
		{"responses unsupported", AccountModePayG, "api_protocol", APIProtocolResponses},
		{"hidden stale endpoint", MiMoModeDesktop, "api_base_urls", map[string]any{APIProtocolAnthropic: "https://api.xiaomimimo.com/anthropic"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := miMoTestAccount(tc.mode)
			a.Credentials[tc.field] = tc.value
			require.Error(t, ValidateMiMoAccount(a))
			_, _, err := a.miMoCredential()
			require.Error(t, err)
		})
	}
	a := miMoTestAccount(MiMoModeTokenPlan)
	a.Credentials["api_key"] = "ttp-team-test"
	a.Credentials["base_url"] = "https://token-plan-sgp.xiaomimimo.com/v1"
	require.NoError(t, ValidateMiMoAccount(a))
}

func TestMiMoForwardingModes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{MiMoModeDesktop, MiMoModeTokenPlan, AccountModePayG} {
		t.Run(mode, func(t *testing.T) {
			a := miMoTestAccount(mode)
			body := []byte(`{"model":"mimo-v2.6-flash","messages":[{"role":"user","content":"ping"}],"stream":false}`)
			if mode == MiMoModeDesktop {
				body = bytes.ReplaceAll(body, []byte("mimo-v2.6-flash"), []byte("mimo-pro"))
			}
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
			c.Request.Header.Set("Cookie", "client-session=must-not-forward")
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"mimo-test","object":"chat.completion","model":"mimo-pro","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))}}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			result, err := svc.forwardAsRawChatCompletions(context.Background(), c, a, body, "")
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, 2, result.Usage.InputTokens)
			require.Contains(t, rec.Body.String(), "OK")
			switch mode {
			case MiMoModeDesktop:
				require.Equal(t, "https://mimo-server-cn.xiaomimimo.com/api/route/chat/completions", upstream.lastReq.URL.String())
				require.Empty(t, upstream.lastReq.Header.Get("Authorization"))
				require.Equal(t, a.GetCredential("mimo_session_cookie"), upstream.lastReq.Header.Get("Cookie"))
				require.Empty(t, a.GetOpenAIProtocolAPIKey())
			case MiMoModeTokenPlan:
				require.Equal(t, "token-plan-cn.xiaomimimo.com", upstream.lastReq.URL.Host)
				require.Empty(t, upstream.lastReq.Header.Get("Cookie"))
				require.Equal(t, "Bearer tp-subscription-test", upstream.lastReq.Header.Get("Authorization"))
			default:
				require.Equal(t, "api.xiaomimimo.com", upstream.lastReq.URL.Host)
				require.Empty(t, upstream.lastReq.Header.Get("Cookie"))
				require.Equal(t, "Bearer sk-metered-test", upstream.lastReq.Header.Get("Authorization"))
			}
			// Responses and Anthropic compatibility fallbacks must resolve the same
			// credential/URL as the raw Chat Completions path (including Desktop).
			credential, target, err := svc.resolveCCFallbackTarget(a)
			require.NoError(t, err)
			require.NotEmpty(t, credential)
			require.Equal(t, upstream.lastReq.URL.String(), target)
		})
	}
}

func TestMiMoDesktopSessionSurvivesSanitizingAndMaskedEdits(t *testing.T) {
	a := miMoTestAccount(MiMoModeDesktop)
	a.Credentials = SanitizeStoredCredentials(a.Platform, a.Credentials)
	require.NoError(t, ValidateMiMoAccount(a))
	require.False(t, isUpstreamBillingProbeAccount(a))
	require.True(t, IsSensitiveCredentialKey("mimo_session_cookie"))
	a.Credentials = MergePreservingSensitiveCreds(a.Credentials, map[string]any{"account_mode": MiMoModeDesktop, "api_protocol": APIProtocolAdaptive})
	require.NoError(t, ValidateMiMoAccount(a))
	require.NotEmpty(t, a.GetCredential("mimo_session_cookie"))
}

func TestMiMoDesktopModelSync(t *testing.T) {
	a := miMoTestAccount(MiMoModeDesktop)
	svc := &AccountTestService{cfg: rawChatCompletionsTestConfig()}
	req, err := svc.buildUpstreamModelsRequest(context.Background(), a)
	require.NoError(t, err)
	require.Equal(t, "https://mimo-server-cn.xiaomimimo.com/api/model/list", req.URL.String())
	require.Empty(t, req.Header.Get("Authorization"))
	require.Equal(t, a.GetCredential("mimo_session_cookie"), req.Header.Get("Cookie"))
	raw, err := normalizeMiMoDesktopModels([]byte(`{"code":0,"data":{"models":[{"modelName":"mimo-v2.6-flash","modelType":"TEXT"},{"modelName":"mimo-tts","modelType":"TTS"}]}}`))
	require.NoError(t, err)
	ids, err := extractUpstreamModelIDs(raw)
	require.NoError(t, err)
	require.Equal(t, []string{"mimo-v2.6-flash"}, ids)
	_, err = normalizeMiMoDesktopModels([]byte(`{"code":401,"data":null}`))
	require.Error(t, err)
}

func TestMiMoDesktopAdaptiveConnectionProbe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/test", nil)
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: [DONE]\n\n"))}}
	svc := &AccountTestService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	require.NoError(t, svc.testCNProviderAdaptiveConnection(c, miMoTestAccount(MiMoModeDesktop), "mimo-v2.6-flash", "Reply OK"))
	require.NotNil(t, upstream.lastReq)
	require.Empty(t, upstream.lastReq.Header.Get("Authorization"))
	require.NotEmpty(t, upstream.lastReq.Header.Get("Cookie"))
}
