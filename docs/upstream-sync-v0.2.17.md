# v0.2.17 上游同步记录

日期：2026-10-16。源码版本：`0.2.17-laien.9`。

## 合并范围

- 定制基线：`87d961b2d7fb13e2b86c18603a66dc640f9335a9`（`v0.2.15-laien.9`）。
- 上游：`Wei-Shaw/sub2api` 的 `v0.2.17`，提交 `060d0160417782cd962a0c7f460f6cd4565d4916`。
- 合并从共同祖先到 v0.2.17 的完整更新，包括 v0.2.16 的改动，不只挑选 Gemini 提交。
- 唯一的 Git 文本冲突是 `backend/cmd/server/VERSION`，采用定制版本号。上游标签内的 VERSION 文件仍是 0.2.16，不能直接照搬。

## 保留的定制行为

| 范围 | 保留行为 |
| --- | --- |
| Grok 图片 | 默认 `response_format=b64_json`，保留显式参数，图片结果规范为 OpenAI Images 响应，保留已有几何参数处理 |
| MiMo 授权与凭证 | Desktop 官方登录及会话 Cookie、Token Plan 和按量付费三种模式隔离；脱敏、编辑保留、模型同步保持原有逻辑 |
| MiMo 额度 | Desktop 周额度展示；额度耗尽 403 按重置时间冷却，不误判为永久禁用 |
| MiMo 模型 | 保留 `mimo-v2.6-pro-ultraspeed` 预设 |
| Codex 工具转换 | 保留 namespace 内 custom `exec` 工具、工具历史和回程转换；仅对 MiMo 剔除正文回显的 `<tool_call>` 块 |
| 前端定制 | 创建 API Key 直接选分组、仪表盘隐藏平台细分等改动保留 |
| 镜像发布 | 保留定制仓库发布流程、显式发布开关和禁止推送上游命名空间的限制 |

未与上游重叠的 65 个定制文件在初次合并后均与原版逐字一致。后续为通过现有 lint，把 MiMo 登录响应体的关闭改为显式忽略关闭错误；另增强了发布资源复制的测试断言。Grok 图片与 MiMo 业务逻辑未被上游版本替换。

## 同步后的主要能力

- Gemini / Antigravity Argon 模型目录与计费、兼容入口思考参数与流式摘要、模型能力同步。
- Gemini 显式缓存与 Vertex 模型同步、鉴权和错误处理改进。
- 工具结果图片、原生 Gemini functionCall / thoughtSignature、composite 路由修复。
- Responses `created_at` 补齐、结构化输入回退和最小输出 token 配置。
- 上游支付退款、查单、分组多语言等其余更新。

Google One OAuth 的三个默认模型限制仍遵循上游实现；升级不会自动赋予新模型权限。GPT-5.5 订阅退役行为同样随上游合入，仅作用于其指定的 ChatGPT 订阅账号范围。

## 回归验证

- Go 1.27.2：运行全量 `go test -tags=unit ./...` 和 `go test -tags=integration ./...`；修正遗漏 MiMo 的平台目录与设置契约测试，并复测失败包。
- 数据库集成测试使用 Docker 中独立的 PostgreSQL / Redis，包含新缓存表迁移；未连接部署数据库。
- 新增 `TestMiMoResponsesPreservesCustomToolsAndCreatedAt`：通过完整 Responses 入口验证三种模式 × 流式/非流式，覆盖凭证隔离、namespace 内 `exec`、正文回显过滤、usage 和 `created_at` 生命周期。
- 前端运行全量 Vitest；补齐平台限额测试中遗漏的 MiMo，并增加 MiMo 限额编辑保留用例。失败文件复测通过，共覆盖 372 个测试文件、2876 个用例。
- 前端生产构建与 ESLint、后端 golangci-lint v2.14.0（0 issues）、含嵌入前端的二进制构建、版本输出校验。
- 发布辅助测试及部署脚本检查；资源测试改为验证本项目实际使用的 release_matrix 构建上下文，保持 GoReleaser 禁止直接发布镜像的配置。

未调用真实 Grok / MiMo 上游消耗额度；以上功能验证使用现有测试及模拟上游。未发布镜像、推送远端或更新在线服务。部署模板的既有镜像标签不会因为源码合并自动升级。
