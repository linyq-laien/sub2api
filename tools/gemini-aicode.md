# Gemini AI Code 接入

## 403 的原因

`cloudaicompanion.googleapis.com / SUBSCRIPTION_REQUIRED / #3501` 来自 Code Assist 许可证检查。
参考实现调用的是另一产品通道：`https://aicode.googleapis.com/v1alpha:generateContent`。
不能用旧 Gemini CLI 的 refresh token 配 Antigravity 的 client_id 刷新，也不能通过修改 User-Agent 获得许可。

## 接入步骤

1. 使用 Antigravity consumer 客户端导出的凭据，client_id 为 `1071006060591-tmhssin2h21lcre235vtolojh4g403ep.apps.googleusercontent.com`，scope 必须包含 `https://www.googleapis.com/auth/aicode`。
2. 在项目根目录运行：

   ```sh
   python3 tools/import-gemini-aicode.py /path/to/credentials_ag.json /path/to/sub2api-aicode-import.json
   ```

3. 部署本次后端变更后，通过管理员账号导入功能导入生成的 JSON。为新账号绑定 Gemini 分组以及适用的出口代理，使用 `gemini-3.5-flash-lite` 测试。
4. 如修改已有账号，将 credentials.oauth_type 设为 `aicode`，替换为同一 Antigravity 客户端的 refresh_token/access_token/scope，设置 expires_at 为字符串 `0` 以强制刷新。旧账号的错误状态需要在管理界面清除。不要仅修改 oauth_type 而保留旧客户端令牌。

导入文件包含登录凭据，应保存在本地；不要提交到 Git。转换器只生成文件，不调用后台或覆盖账号。
目前采用凭据导入方式：参考代码指出普通可见 scope 的浏览器授权不能保证获得内部 `auth/aicode` 权限，因此没有添加一个无法保证成功的授权按钮。

## 行为与限制

- 显式 `oauth_type=aicode` 或参考客户端的 `client_id` 识别此通道，即使遗留 project_id 存在，也优先使用 AI Code。
- 令牌刷新使用参考客户端配对；缓存按账号隔离，不与旧 project_id 的 Code Assist 令牌共用。
- Gemini 原生、Messages、Chat Completions 及账号连接测试均走相同请求构造，保留图片、视频等 contents 内容。
- 请求顶层使用 `aicode.experience` 和 `entitlement.userTier`，移除与其 oneof 冲突的 model；没有 Code Assist 的 request/project 包装。
- 只有 `USER_TIER_NOT_ENTITLED` 且返回不同的 resolved_tier 时纠正并重试一次。许可证、地域、普通权限错误不会在此适配器中重试。
- 模型列表从 `fetchConfig.availableExperiences` 获取（网关 `/v1beta/models` 与账号编辑页“同步上游支持的模型”按钮同源；AI Code 令牌无 Generative Language 权限，不能用 `/v1beta/models`）。导入默认只启用参考验证过的 `gemini-3.5-flash-lite`，其他 experience 先用同步按钮拉取账号实际可用目录再配置。
- 使用参考验证过的非流式 generateContent，上游完整响应到达后转为单条 SSE 事件供流式客户端使用，不是逐 token 实时输出。
- countTokens 不向其他产品接口回退，返回明确的不支持错误；未实现 AI Code 显式缓存操作。

## 本次验证（2026-11-05）

参考凭据 token refresh 返回 HTTP 200；AI Code fetchConfig/generateContent 返回 HTTP 403，reason 为 LOCATION_NOT_ELIGIBLE，检测到的出口为 CN。这是上游地域限制，与原 Code Assist SUBSCRIPTION_REQUIRED 不同。本机未完成模型回复成功验证，需在该服务支持的实际运行地区验证。

本机 Go 1.27.3 下载遇到证书信任错误，使用缓存 Go 1.27.2 和临时 modfile 运行针对性回归；不修改项目 go.mod 的版本要求。
