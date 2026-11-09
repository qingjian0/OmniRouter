# DeepSeek Android V1 APK — implementation plan

状态：开发分支规划与技术门禁；不是已交付 APK。

目标：为 Android 设备提供独立安装的 DeepSeek 网页会话反向代理。V1 只接入 `chat.deepseek.com` 用户主动登录的网页会话，不提供多 Provider 路由，不要求 Termux，不把 DeepSeek 官方 API Key 当作网页会话替代品。

## 当前可复用的后端

`dev` 已包含 PR #21 合并的 DeepSeek Web 基础实现：

- 固定上游 Origin：`https://chat.deepseek.com`
- 用户授权会话凭据的加密存储、配对和撤销
- `/api/v0/users/current`、`/api/v0/chat_session/create`、`/api/v0/chat/completion` 请求流程
- SSE 帧解析、THINK/RESPONSE patch 解码、OpenAI Chat Completions 流式输出
- SQLite 会话 lineage 和同一会话的串行执行
- 失败关闭的未知事件、上游策略拒绝和未完成流处理

这些是代码实现与本地 fixture 覆盖，不等于真实 DeepSeek 账号验收。真实请求 schema、挑战/策略行为、首轮流式输出、第二轮连续性、重启恢复仍须用用户正常登录的账号验证。不得实现 PoW/CAPTCHA/MFA/WAF 绕过。

## Android 交付架构

优先评估并采用单 APK + Go 原生共享库：

1. Kotlin Android 前台服务负责进程生命周期、通知、启动/停止和 Android 后台限制。
2. Go 后端通过 Android ARM64 `c-shared` / JNI 启动，继续复用现有 Gateway、SQLite、凭据 Vault 和 DeepSeek Web Provider。
3. 管理 UI 由 APK 内置 WebView 加载 loopback 管理页面；服务默认只绑定 `127.0.0.1`，不默认开放 LAN。
4. 用户通过 APK 内的专用 WebView 正常访问并登录 `chat.deepseek.com`。当前分支已实现显式点击“连接此账号”后读取该站点的 `userToken` 与 `ds_session_id`，经 JNI 直接交给 Go 后端的持久加密 Vault；不经过剪贴板、日志或普通 HTTP pairing endpoint。仍需在真实设备验证 DeepSeek 页面当前确实使用这些字段。禁止读取其他站点、导出浏览器配置文件、收集密码/MFA、自动化挑战或伪造身份。
5. Gateway 主密钥和凭据加密密钥必须由 Android Keystore 保护；不得写入日志、导出备份或明文 SharedPreferences。
6. APK 使用 GitHub Actions 构建并发布 arm64 APK 与 SHA-256；用户不需要本机安装 Go/Node/Gradle/NDK。

若 Go `c-shared` + SQLite 在 Android NDK 上存在不可解决的兼容性问题，再评估替代方案；不得用“下载后执行 Termux 二进制”冒充 APK。

## V1 功能边界

- 仅 DeepSeek 网页会话 Provider；禁用/隐藏其他 Provider 类型和调度配置。
- OpenAI 兼容 `POST /v1/chat/completions`、`GET /v1/models`。
- 文本请求、SSE 流式响应、reasoning 与 answer 分流、连续对话。
- 账号连接、会话状态、重新登录、撤销授权。
- 服务启停、健康状态、日志脱敏、数据备份/恢复。
- 本地 loopback 默认；LAN 访问需单独设置并明确风险提示。
- 首版不承诺图片、文件、原生工具调用或搜索引用映射。

## 交付门禁

### A. 后端真实验收
- [ ] 用户正常登录后成功连接账号；不上传密码/MFA/挑战证明。（Android WebView 显式配对代码已实现，实机待验）
- [ ] 一次非流式文本请求成功。
- [ ] 一次 SSE 请求完整收到经验证的终止事件。
- [ ] 同一 conversation ID 的第二轮正确延续上游会话。
- [ ] 服务重启后凭据解密恢复、lineage 连续性正确。
- [ ] 撤销后请求失败关闭；过期/限流/策略拒绝可区分。
- [ ] 所有日志、错误和管理 API 均不泄露 token/cookie/session ID。

### B. Android APK
- [ ] Gradle 构建 arm64 APK 成功并可安装。（早期 debug 构建已通过；含当前 JNI pairing / UI 改动的最新构建仍在 CI）
- [ ] Android 设备上 JNI 启停 Go 服务；无 Termux 依赖。
- [ ] 前台服务通知、停止、进程重启和 Android 16 后台行为通过实机验证。
- [ ] WebView 只允许 DeepSeek 登录域名和本地管理页面；桥接仅在精确来源启用。
- [ ] Android Keystore 密钥保护、应用数据隔离和备份策略通过审查。
- [ ] 本地管理页面、健康检查和 OpenAI 兼容 API 在实机可用。

### C. 发布
- [ ] GitHub Actions 构建、单元测试、静态检查和 APK 签名流程通过。
- [ ] 生成 APK、版本说明和 SHA-256。
- [ ] 在目标 Android 设备完成安装、启动、连接、聊天、重启和撤销验收。
- [ ] 仅在所有必要门禁通过后标记 V1 正式可用。

## 当前状态

- Go/Linux 测试与 Android ARM64 Termux 二进制交叉编译曾通过；这不是 APK 构建。
- `android-dev` 目前是 Termux 压缩包，不是 APK。
- 本分支从 `dev` 创建，隔离 Android APK 工作，不改动 `main`。
