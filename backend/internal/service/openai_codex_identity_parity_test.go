package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/tidwall/gjson"
)

// runCodexParityForward 以同一客户端 session 发起一轮透传请求，返回出站请求。
func runCodexParityForward(t *testing.T, extra map[string]any, turnID string) (*http.Request, []byte) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/backend-api/codex/responses", bytes.NewReader(nil))
	SID := "8db1b6ec-5dad-48f7-9619-ed637984cd29"
	c.Request.Header.Set("User-Agent", "codex_exec/0.166.0 (Mac OS 27.0.1; arm64) iTerm.app (codex_exec; 0.166.0)")
	c.Request.Header.Set("originator", "codex_exec")
	c.Request.Header.Set("session-id", SID)
	c.Request.Header.Set("thread-id", SID)
	c.Request.Header.Set("x-client-request-id", SID)
	c.Request.Header.Set("x-codex-beta-features", "remote_compaction_v2")
	c.Request.Header.Set("x-codex-window-id", SID+":0")
	c.Request.Header.Set("x-codex-turn-metadata", `{"installation_id":"a66782fe-4f47-4ba8-a89d-721bbde48e4d","session_id":"`+SID+`","thread_id":"`+SID+`","turn_id":"`+turnID+`","window_id":"`+SID+`:0","window_number":0,"request_kind":"turn","thread_source":"user","turn_trigger":"exec","sandbox_mode":"danger-full-access"}`)
	c.Request.Header.Set("x-openai-internal-codex-responses-lite", "true")

	body := []byte(`{"model":"gpt-6.1-sol","instructions":"You are a helpful assistant.","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Reply with exactly: OK"}]}],"stream":true,"store":false}`)

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid"}},
		Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	if extra == nil {
		extra = map[string]any{}
	}
	extra["openai_passthrough"] = true
	account := newTestOAuthAccount(99001, extra)
	account.Name = "parity"
	account.Status = StatusActive
	account.Schedulable = true
	account.Concurrency = 1
	account.Credentials = map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "279b0d1a-247b-4653-8808-b4b18baf848c"}

	if _, err := svc.Forward(context.Background(), c, account, body); err != nil {
		t.Fatalf("forward: %v", err)
	}
	if upstream.lastReq == nil {
		t.Fatal("no upstream request captured")
	}
	return upstream.lastReq, upstream.lastBody
}

// TestCodexCLIIdentityParity_SessionStableAcrossTurns 验证透传出站身份的
// 会话级稳定性与官方 CLI wire 形态对齐：
//   - session-id / thread-id / x-client-request-id 三件套必带；
//   - session == thread == prompt_cache_key（codex exec 主线程形态）且跨
//     turn 稳定；turn_id 按 turn 派生；
//   - x-codex-window-id 与 metadata.window_id 恒为 thread+":0"；
//   - CLI 不发送的 session_id / conversation_id 下划线头不出站。
func TestCodexCLIIdentityParity_SessionStableAcrossTurns(t *testing.T) {
	reqA, bodyA := runCodexParityForward(t, nil, "11111111-1111-4111-8111-111111111111")
	reqB, bodyB := runCodexParityForward(t, nil, "22222222-2222-4222-8222-222222222222")
	reqB2, bodyB2 := runCodexParityForward(t, nil, "22222222-2222-4222-8222-222222222222")

	sessionA := reqA.Header.Get("session-id")
	assert.NotEmpty(t, sessionA)
	threadA := reqA.Header.Get("thread-id")
	// 会话级稳定：不同 turn / 同 turn 重放，session 与 thread 不变
	assert.Equal(t, sessionA, reqB.Header.Get("session-id"))
	assert.Equal(t, sessionA, reqB2.Header.Get("session-id"))
	assert.Equal(t, threadA, reqB.Header.Get("thread-id"))
	assert.Equal(t, threadA, reqB2.Header.Get("thread-id"))
	// CLI 相等关系：exec 主线程形态 session == thread
	assert.Equal(t, sessionA, threadA)
	// 三件套与窗口不变式
	assert.Equal(t, threadA, reqA.Header.Get("x-client-request-id"))
	assert.Equal(t, threadA+":0", reqA.Header.Get("x-codex-window-id"))
	// turn 级派生：换 turn 变化，同 turn 重放稳定
	turnA := gjson.Get(reqA.Header.Get("x-codex-turn-metadata"), "turn_id").String()
	turnB := gjson.Get(reqB.Header.Get("x-codex-turn-metadata"), "turn_id").String()
	turnB2 := gjson.Get(reqB2.Header.Get("x-codex-turn-metadata"), "turn_id").String()
	assert.NotEqual(t, turnA, turnB)
	assert.Equal(t, turnB, turnB2)
	// metadata 与头同源
	md := gjson.Parse(reqA.Header.Get("x-codex-turn-metadata"))
	assert.Equal(t, sessionA, md.Get("session_id").String())
	assert.Equal(t, threadA, md.Get("thread_id").String())
	assert.Equal(t, threadA+":0", md.Get("window_id").String())
	// installation 跨 turn 稳定
	assert.Equal(t,
		gjson.Get(reqA.Header.Get("x-codex-turn-metadata"), "installation_id").String(),
		gjson.Get(reqB.Header.Get("x-codex-turn-metadata"), "installation_id").String())
	// CLI 不发送下划线头
	assert.Empty(t, reqA.Header.Get("session_id"))
	assert.Empty(t, reqA.Header.Get("conversation_id"))
	// prompt_cache_key = 派生 session（SessionDerived 策略）
	assert.Equal(t, sessionA, gjson.GetBytes(bodyA, "prompt_cache_key").String())
	assert.Equal(t, sessionA, gjson.GetBytes(bodyB, "prompt_cache_key").String())
	assert.Equal(t, sessionA, gjson.GetBytes(bodyB2, "prompt_cache_key").String())
}

// TestCodexCLIIdentityParity_SkippedForFingerprintMode 指纹收敛模式自生成
// 完整会话头，parity 不应介入（窗口形态由收敛逻辑负责）。
func TestCodexCLIIdentityParity_SkippedForFingerprintMode(t *testing.T) {
	req, _ := runCodexParityForward(t, map[string]any{codexFingerprintModeExtraKey: "session"}, "11111111-1111-4111-8111-111111111111")
	// 收敛模式保留 session_id 下划线头（既有断言形态），且窗口为 thread:0
	assert.NotEmpty(t, req.Header.Get("session_id"))
	thread := req.Header.Get("thread-id")
	assert.NotEmpty(t, thread)
	assert.Equal(t, thread+":0", req.Header.Get("x-codex-window-id"))
}

// TestCodexCLIIdentityParity_IgnoresNonCodexClient 非 Codex 形态请求
// （无 session-id / turn-metadata）不做 parity 重建。
func TestCodexCLIIdentityParity_IgnoresNonCodexClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/backend-api/codex/responses", bytes.NewReader(nil))
	c.Request.Header.Set("User-Agent", "curl/8.0")

	body := []byte(`{"model":"gpt-6.1-sol","instructions":"x","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],"stream":true,"store":false}`)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: [DONE]\n\n")),
	}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	account := newTestOAuthAccount(99002, map[string]any{"openai_passthrough": true})
	account.Status = StatusActive
	account.Schedulable = true
	account.Concurrency = 1
	account.Credentials = map[string]any{"access_token": "oauth-token", "chatgpt_account_id": "acc"}

	if _, err := svc.Forward(context.Background(), c, account, body); err != nil {
		t.Fatalf("forward: %v", err)
	}
	assert.Empty(t, upstream.lastReq.Header.Get("session-id"))
	assert.False(t, gjson.GetBytes(upstream.lastBody, "prompt_cache_key").Exists())
}
