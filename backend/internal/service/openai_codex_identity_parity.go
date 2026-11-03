package service

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// codexCLIIdentityParity 是与官方 Codex CLI wire 形态对齐的一组出站会话标识。
// 值与 x-codex-turn-metadata 的账号身份隔离派生同源（跨 turn 会话级稳定，
// 换 API key / 账号则隔离）。
type codexCLIIdentityParity struct {
	sessionID string
	threadID  string
	windowID  string // threadID + ":0"，CLI 的窗口编码不变式
}

// resolveCodexCLIIdentityParity 由客户端原始请求推导 parity 标识集合。
// 官方 CLI 的相等关系在此保持：客户端 session == thread 时（codex exec /
// tui 主线程形态），派生后的 session 与 thread 也相等；window 恒为
// thread+":0"。
//
// 返回 nil 表示不适用：非 OAuth-like 账号、无身份隔离域、compact 路径，
// 或请求本身不是官方 CLI 形态——只认连字符 session-id 头或 turn-metadata
// （CLI 恒发二者）；仅带下划线 session_id 的 agent-identity / OpenCode 形态
// 保持 sub2api 原有出站（下划线头隔离），不套用 CLI wire 形态。
func resolveCodexCLIIdentityParity(clientHeaders http.Header, account *Account, apiKeyID int64) *codexCLIIdentityParity {
	if account == nil || !account.UsesOpenAICodexProtocol() {
		return nil
	}
	if codexAccountIdentityNamespace(account) == "" {
		return nil
	}
	sess := strings.TrimSpace(clientHeaders.Get("session-id"))
	metadataSession := ""
	metadataThread := ""
	if raw := strings.TrimSpace(clientHeaders.Get(openAIWSTurnMetadataHeader)); raw != "" {
		var m map[string]any
		if json.Unmarshal([]byte(raw), &m) == nil {
			metadataSession, _ = m["session_id"].(string)
			metadataThread, _ = m["thread_id"].(string)
		}
	}
	if sess == "" && metadataSession == "" {
		return nil
	}
	if sess == "" {
		sess = metadataSession
	}
	thread := strings.TrimSpace(clientHeaders.Get("thread-id"))
	if thread == "" {
		thread = metadataThread
	}
	if thread == "" {
		thread = sess
	}
	derivedSession := scopeCodexAccountIdentityValue(account, apiKeyID, "session", sess)
	if derivedSession == "" {
		return nil
	}
	derivedThread := scopeCodexAccountIdentityValue(account, apiKeyID, "thread", thread)
	if derivedThread == "" {
		return nil
	}
	if thread == sess {
		derivedThread = derivedSession
	}
	return &codexCLIIdentityParity{
		sessionID: derivedSession,
		threadID:  derivedThread,
		windowID:  derivedThread + ":0",
	}
}

// applyCodexCLIIdentityHeaderParity 将 OAuth 透传出站的会话身份头对齐官方
// Codex CLI 的 wire 形态（codex-rs default_client.rs / client.rs）：
//
//   - session-id / thread-id / x-client-request-id(=thread) 必带，值与
//     turn-metadata 同源派生（透传白名单只放行下划线 session_id，真实 CLI
//     发送的连字符三件套历史上被整体剥掉）；
//   - x-codex-window-id 与 metadata.window_id 恒为 thread+":0"（字段级
//     哈希派生会得到裸 UUID，丢失 CLI 的窗口编码不变式）；
//   - CLI 不发送下划线 session_id / conversation_id 头，一并移除。
//
// 指纹收敛模式（device/session/full）已自行生成完整会话头，此函数跳过。
func applyCodexCLIIdentityHeaderParity(h http.Header, clientHeaders http.Header, account *Account, apiKeyID int64, fpIDs *codexFingerprintIDs) {
	if h == nil || fpIDs != nil {
		return
	}
	parity := resolveCodexCLIIdentityParity(clientHeaders, account, apiKeyID)
	if parity == nil {
		return
	}
	h.Set("session-id", parity.sessionID)
	h.Del("session_id")
	h.Del("conversation_id")
	h.Set("thread-id", parity.threadID)
	h.Set("x-client-request-id", parity.threadID)
	h.Set("x-codex-window-id", parity.windowID)
	rewriteCodexTurnMetadataFields(h, map[string]any{
		"session_id": parity.sessionID,
		"thread_id":  parity.threadID,
		"window_id":  parity.windowID,
	})
}

// ensureCodexPromptCacheKeyParity 对齐官方 CLI 的 prompt_cache_key 默认策略
// （codex-rs core/src/client.rs PromptCacheKey::SessionDerived：请求体默认
// 携带与会话同源的缓存键）。仅对 Codex 形态请求（session-id 头或
// turn-metadata）且客户端未携带时注入派生 session id；携带时上游侧已由
// applyCodexAccountIdentityClientMetadataRaw 完成同源 scope。
func ensureCodexPromptCacheKeyParity(body []byte, clientHeaders http.Header, account *Account, apiKeyID int64) []byte {
	if account == nil || !account.UsesOpenAICodexProtocol() {
		return body
	}
	if gjson.GetBytes(body, "prompt_cache_key").Exists() {
		return body
	}
	parity := resolveCodexCLIIdentityParity(clientHeaders, account, apiKeyID)
	if parity == nil {
		return body
	}
	next, err := sjson.SetBytes(body, "prompt_cache_key", parity.sessionID)
	if err != nil {
		return body
	}
	return next
}
