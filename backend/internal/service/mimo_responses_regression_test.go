//go:build unit

package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Exercise the complete Responses entry point after upstream merges: MiMo's
// credential isolation, Codex custom tools and echo filtering must coexist with
// the upstream Responses lifecycle/created_at contract.
func TestMiMoResponsesPreservesCustomToolsAndCreatedAt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{MiMoModeDesktop, MiMoModeTokenPlan, AccountModePayG} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", mode, stream), func(t *testing.T) {
				account := miMoTestAccount(mode)
				body := []byte(fmt.Sprintf(`{"model":"mimo-v2.6-flash","input":"run pwd","stream":%t,"tools":[{"type":"namespace","name":"functions","tools":[{"type":"custom","name":"exec","format":{"type":"text"}}]}]}`, stream))
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
				c.Request.Header.Set("Cookie", "client-session=must-not-forward")
				upstreamBody := `{"id":"mimo-tools","choices":[{"message":{"role":"assistant","content":"Running.<tool_call><function=exec>pwd</function></tool_call>","tool_calls":[{"id":"call_exec","type":"function","function":{"name":"exec","arguments":"{\"input\":\"pwd\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`
				contentType := "application/json"
				if stream {
					contentType = "text/event-stream"
					upstreamBody = strings.Join([]string{
						`data: {"id":"mimo-tools","choices":[{"index":0,"delta":{"content":"Running.<tool_"}}]}`,
						`data: {"id":"mimo-tools","choices":[{"index":0,"delta":{"content":"call><function=exec>pwd</function></tool_call>","tool_calls":[{"index":0,"id":"call_exec","type":"function","function":{"name":"exec","arguments":"{\"input\":\"pwd\"}"}}]},"finish_reason":"tool_calls"}]}`,
						`data: {"choices":[],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`,
						"data: [DONE]",
						"",
					}, "\n\n")
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{contentType}},
					Body:       io.NopCloser(strings.NewReader(upstreamBody)),
				}}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
				result, err := svc.Forward(context.Background(), c, account, body)
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, rec.Code)
				require.Equal(t, 2, result.Usage.InputTokens)
				require.Equal(t, 3, result.Usage.OutputTokens)
				require.Equal(t, "exec", gjson.GetBytes(upstream.lastBody, "tools.0.function.name").String())
				if mode == MiMoModeDesktop {
					require.Equal(t, "mimo-server-cn.xiaomimimo.com", upstream.lastReq.URL.Host)
					require.Equal(t, account.GetCredential("mimo_session_cookie"), upstream.lastReq.Header.Get("Cookie"))
					require.Empty(t, upstream.lastReq.Header.Get("Authorization"))
				} else {
					require.Empty(t, upstream.lastReq.Header.Get("Cookie"))
					require.Equal(t, "Bearer "+account.GetCredential("api_key"), upstream.lastReq.Header.Get("Authorization"))
				}

				response := gjson.Parse(rec.Body.String())
				if stream {
					var createdAt int64
					var text strings.Builder
					var completed bool
					for _, payload := range sseDataPayloads(rec.Body.String()) {
						event := gjson.Parse(payload)
						if event.Get("response").Exists() {
							ts := event.Get("response.created_at").Int()
							require.Positive(t, ts)
							if createdAt == 0 {
								createdAt = ts
							}
							require.Equal(t, createdAt, ts)
						}
						if event.Get("type").String() == "response.output_text.delta" {
							_, _ = text.WriteString(event.Get("delta").String())
						}
						if event.Get("type").String() == "response.completed" {
							response = event.Get("response")
							completed = true
						}
					}
					require.True(t, completed)
					require.Equal(t, "Running.", text.String())
				}
				require.Positive(t, response.Get("created_at").Int())
				var calls, messages int
				for _, item := range response.Get("output").Array() {
					switch item.Get("type").String() {
					case "message":
						messages++
						require.Equal(t, "Running.", item.Get("content.0.text").String())
					case "custom_tool_call":
						calls++
						require.Equal(t, "exec", item.Get("name").String())
						require.Equal(t, "call_exec", item.Get("call_id").String())
						require.Equal(t, "pwd", item.Get("input").String())
					default:
						t.Fatalf("unexpected output item: %s", item.Raw)
					}
				}
				require.Equal(t, 1, calls)
				require.Equal(t, 1, messages)
			})
		}
	}
}
