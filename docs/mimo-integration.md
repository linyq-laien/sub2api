# Xiaomi MiMo 第一版接入

账号平台选择 **Xiaomi MiMo**，再选择接入方式。三种方式共享 `platform=mimo`，使用独立的 `credentials.account_mode` 和凭证字段；内部账号类型统一为 `apikey`，不代表 Desktop 使用 API Key。

| 接入方式 | account_mode | 凭证 | 默认 Chat Completions 端点 | 上游扣费来源 |
| --- | --- | --- | --- | --- |
| MiMo Desktop 会员 | `desktop` | `mimo_session_cookie`，业务会话 Cookie | `https://mimo-server-cn.xiaomimimo.com/api/route/chat/completions` | Desktop 会员额度 |
| Token Plan | `token_plan` | `api_key`，`tp-` 或团队 `ttp-` | `https://token-plan-cn.xiaomimimo.com/v1` | Token Plan 套餐 |
| 按量付费 | `payg` | `api_key`，`sk-` | `https://api.xiaomimimo.com/v1` | 开放平台余额 |

这里的 Token Plan 对应需求中所称的 Token Play。Token Plan 可按购买区域改为 `token-plan-sgp.xiaomimimo.com` 或 `token-plan-ams.xiaomimimo.com`。Token Plan 和按量付费还支持各自域名下的 `/anthropic` 端点。Desktop 只向上游发送 Chat Completions；Responses 和 Anthropic 入站沿用项目现有协议转换。

## 账号与凭证

- 创建或编辑时切换接入方式会清空凭证输入并重置端点，必须填写新方式的凭证。后端对创建、编辑、批量修改和转发做模式校验，拒绝混用官方端点、Key 类型与 Desktop 会话。
- Desktop 默认使用「小米账号授权」：点击「登录 / 重新授权」，打开小米官方页面登录确认，或使用小米应用扫描二维码。页面显示授权成功后，点击创建/保存即可关联会员会话，无需复制 Cookie。小米账号密码只在小米官方页面输入。
- 授权成功后，浏览器仅提交 `mimo_login_session_id`，后端将它兑换为业务会话。授权编号绑定发起操作的管理员，5 分钟有效，保存成功即清除；取消、关闭弹窗或切换接入方式会撤销待保存的授权。失败的账号保存可在有效期内重试。
- 这使用 Xiaomi Passport SSO 和 Desktop 业务会话，并非 Codex OAuth 的 access/refresh token 协议。当前支持中国区，没有自动刷新；会话失效后点击重新授权。MiMo Code 的 API Key 授权流程不用于 Desktop 会员。
- 授权过程仅在当前服务进程内暂存，多副本部署需将授权及账号保存请求固定到同一实例；服务重启后需要重新授权。
- 保留折叠的「手动导入 Cookie（备用）」入口，接受业务域名有效的 Cookie 请求头，例如 `serviceToken=…; userId=…`，不能填小米密码、`sk-` 或 `tp-`。后端只保留 Desktop 业务 Cookie，不保存小米账号 `passToken`。转发使用 Cookie 鉴权，不发送 Bearer API Key。
- Desktop 会话存放在专用敏感字段，管理接口不返回明文；编辑时不重新授权且不填备用凭证，可保留已有会话。
- Desktop 模型同步使用 `/api/model/list`，只导入文本模型；连接测试优先使用实际模型目录。它不启用 API Key 账单探测，也不将会员额度伪装成开放平台余额。
- Desktop 用量窗口：使用业务会话调用 `GET /api/user/usage`（与 Desktop 客户端「用量」页同源），响应 `data.percent` 是**剩余**百分比，`data.resetAt` 是周窗口重置时间（秒级时间戳）。后端换算为已用百分比写入 `mimo_weekly_used_percent` / `mimo_weekly_reset_at` 快照，账号列表「用量窗口」展示周额度条；周期任务与 Kimi/智谱 Coding Plan 同批刷新。Token Plan 与按量付费不显示此窗口。
- Desktop 会员额度耗尽时上游返回 403 `subscription_quota_exhausted`（`biz_code=30011`）。该错误按配额耗尽处理：有快照时限流到周窗口重置点，无快照时临时停调 10 分钟，不计入 403 连续失败禁用计数，账号不会因此被永久标记错误。
- 没有绑定模型映射时，默认测试模型为 `mimo-v2.6-flash`。可同步上游目录以获取当前可用模型。

## 本地验证实例

本次实例使用隔离的 PostgreSQL、Redis 和本地二进制：

- 页面：`http://127.0.0.1:18080`
- PostgreSQL：`127.0.0.1:15432`；Redis：`127.0.0.1:16379`
- 运行配置与管理员登录信息：`.local/mimo/runtime.json`，仅本机可读。
- 服务日志：`.local/mimo/server.log`；重启脚本：`python3 .local/mimo/start.py`。
- `.local/` 已忽略，不提交本地账号、会话、数据库或运行产物。

2026-10-12 已经用户授权导入当前 Desktop 会话并记录本地合规协议确认。真实连接测试调用 `mimo-v2.6-flash` 返回 `OK`，SSE 结果为 `test_complete.success=true`。Token Plan 与按量付费没有真实测试密钥，使用模拟上游验证请求端点、凭证隔离、响应和 Token 用量解析；不宣称其真实套餐已验证。

已实测生成小米真实授权入口并打开官方登录页面。授权确认、业务会话兑换、管理员隔离、过期/取消、凭证不返回浏览器的流程另有模拟测试覆盖；用户于 2026-10-13 完成本地功能调试并确认无明显问题。

相关自动测试覆盖：三种模式的转发与非法组合、Desktop 会话脱敏和编辑保留、扫码授权生命周期、模型目录转换、连接测试、前端创建和切换方式、平台清单、镜像发布保护。

## 镜像发布

发布目标为 `ghcr.io/linyq-laien/sub2api`，首版标签为 `0.2.15-mimo.1`。部署用 Compose 和 Apple Container 默认使用该固定版本；开发用 `docker-compose.dev.yml` 仍构建本地镜像。首次推送会自动创建 GHCR package，默认私有；部署主机需使用具有 `read:packages` 权限的凭证登录 GHCR。

发布脚本 `.github/release-tools/release-images.sh` 默认 `DRY_RUN=true`，输出本地 OCI 产物。真实发布必须同时显式设置：

```text
IMAGE_PUBLISH_ENABLED=true
IMAGE_REPOSITORY=registry.example.com/your-namespace/sub2api
DRY_RUN=false
```

`IMAGE_REPOSITORY` 不含标签或协议。脚本拒绝原上游 Docker Hub / GHCR 命名空间。GoReleaser 配置不再直接推送 Docker 镜像；镜像统一由上述脚本处理。

GitHub Actions 需要设置仓库变量 `IMAGE_REPOSITORY`、`IMAGE_REGISTRY`、`IMAGE_PUBLISH_ENABLED`，以及 Secrets `IMAGE_REGISTRY_USERNAME`、`IMAGE_REGISTRY_PASSWORD`；只有手动运行 release workflow 且明确关闭 dry run 才发布。本仓库已配置上述目标，后续版本仍需手动触发发布。

## 参考

- [MiMo Desktop 官方接入文档](https://mimo.mi.com/docs/zh-CN/tokenplan/integration/mimo-desktop)
- [MiMo API 首次调用](https://mimo.mi.com/docs/zh-CN/quick-start/summary/first-api-call)
- Desktop 会话及模型目录协议依据本机 Xiaomi MiMo 客户端实现和本次真实请求核验。
