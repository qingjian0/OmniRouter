# OmniRouter V1 installation and first run

## Build from source

The Go server has no Node.js runtime dependency. The Vue 3 + TypeScript admin
console is compiled into the Go binary at build time. Source builds therefore
need Go 1.23+, Node.js 22+, and npm:

```sh
cd web
npm install --no-audit --no-fund
npm run build
cd ..
go test ./...
go build -o omnirouter ./cmd/omnirouter
```

Prebuilt release binaries already contain the compiled admin console and do not
require Node.js, npm, Python, or a Java runtime.

## Release artifacts

Choose the binary for the target platform:

| Platform | Asset |
|---|---|
| Linux x86-64 | `omnirouter-v1.0.0-linux-amd64.tar.gz` |
| Linux ARM64 / Termux candidate | `omnirouter-v1.0.0-linux-arm64.tar.gz` |
| Windows x86-64 | `omnirouter-v1.0.0-windows-amd64.zip` |
| macOS Intel | `omnirouter-v1.0.0-darwin-amd64.tar.gz` |
| macOS Apple Silicon | `omnirouter-v1.0.0-darwin-arm64.tar.gz` |

Replace the version in the asset name with the published release version.
Verify downloads against `SHA256SUMS` before running them.

The Linux ARM64 binary is the intended Termux candidate, not an Android APK.
It must pass the separate real-device acceptance test before Android/Termux
support is advertised.

## Configure a Provider

For the official DeepSeek API, set `OMNI_PROVIDER_KIND=deepseek` and use
`https://api.deepseek.com` (without a `/v1` suffix). The adapter discovers
current models from the authenticated `GET /models` endpoint and maps context,
modalities, reasoning-effort levels, and protocol capabilities. Its offline
fallback catalogue uses `deepseek-flash` and `deepseek-v4-pro`; retired names
`deepseek-chat` and `deepseek-reasoner` should not be used for new deployments.

1. Extract the archive.
2. Copy `omnirouter.env.example` to `.env`.
3. Replace the Provider API key and generate both gateway and credential
   encryption keys. Never commit the resulting `.env` file.
4. Keep `OMNI_ADDR=127.0.0.1:8080` for local-only access by default.

Generate keys:

```sh
openssl rand -hex 32
openssl rand -base64 32
```

The first value is suitable for `OMNI_GATEWAY_API_KEY`; the second is the
Base64-encoded 32-byte `OMNI_CREDENTIAL_ENCRYPTION_KEY`. Back up the
credential-encryption key separately: losing it makes persisted encrypted
Provider credentials unreadable.

### Linux / macOS / Termux

From the extracted directory:

```sh
chmod +x ./omnirouter
cp omnirouter.env.example .env
# Edit .env and replace all placeholders.
set -a
. ./.env
set +a
./omnirouter
```

### Windows PowerShell

Copy the example file and set the same variables in the current PowerShell
session (or use a trusted service manager to inject them):

```powershell
Copy-Item .\omnirouter.env.example .\.env
$env:OMNI_ADDR = "127.0.0.1:8080"
$env:OMNI_DATABASE_PATH = ".\data\omnirouter.db"
$env:OMNI_GATEWAY_API_KEY = "<generated-gateway-key>"
$env:OMNI_CREDENTIAL_ENCRYPTION_KEY = "<generated-base64-32-byte-key>"
$env:OMNI_PROVIDER_KIND = "deepseek"
$env:OMNI_PROVIDER_ID = "deepseek"
$env:OMNI_PROVIDER_NAME = "DeepSeek"
$env:OMNI_PROVIDER_BASE_URL = "https://api.deepseek.com"
$env:OMNI_PROVIDER_API_KEY = "<provider-api-key>"
$env:OMNI_PROVIDER_MODELS = "deepseek-flash,deepseek-v4-pro"
.\omnirouter.exe
```

## Verify the service

Check the embedded version and build commit before starting:

```sh
./omnirouter --version
```

On Windows use `.\omnirouter.exe --version` in PowerShell. Then check the service:

```sh
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz
curl http://127.0.0.1:8080/v1/models \
  -H "Authorization: Bearer $OMNI_GATEWAY_API_KEY"
```

Model capabilities are explicit routing declarations. Declare `streaming` for every model that should receive SSE requests, and declare `tools`/`vision` only when the upstream model supports them. The gateway key protects `/v1/*` and `/admin/*` when configured. Health
endpoints and the embedded admin page are intentionally separate. Do not
expose the service directly to the public internet. For LAN access, use a
trusted TLS reverse proxy and firewall allow-listing.

## Data and upgrades

- SQLite defaults to `data/omnirouter.db`; keep the data directory writable.
- Stop OmniRouter before making a simple file-level database backup.
- Preserve the exact credential-encryption key across restarts and upgrades.
- Upgrade by backing up the database, replacing the binary, reviewing release
  notes and configuration changes, then starting the new binary.
- Do not delete the database or encryption key as part of an ordinary upgrade.

## Operational scripts

The release archives include these optional Bash scripts:

- `backup-sqlite.sh` and `restore-sqlite.sh`: consistent SQLite snapshot, checksum, integrity verification, and restore. See [DATABASE-BACKUP-RESTORE.md](DATABASE-BACKUP-RESTORE.md). Requires the `sqlite3` CLI.
- `provider-smoke.sh`: tests OpenAI, Anthropic Messages, and Gemini text routes in non-streaming and SSE modes through one configured OpenAI-compatible upstream.
- `termux-smoke.sh`: minimum loopback, SQLite, and graceful-shutdown check to run directly on a Termux device.

Windows users can run these scripts under Git Bash or WSL; the prebuilt Windows binary itself does not require Bash.

## API surface

- OpenAI-compatible: `GET /v1/models`, `POST /v1/chat/completions`
- Native Responses API (DeepSeek adapter): `POST /v1/responses`
- Anthropic Messages (native DeepSeek adapter or protocol-translation fallback): `POST /v1/messages`
- Anthropic Messages subset: `POST /v1/messages`
- Gemini subset: `POST /v1beta/models/{model}:generateContent` and
  `streamGenerateContent`
- Operations: `/healthz`, `/readyz`, and authenticated `/admin/*` endpoints

Consult the repository README and release notes for the exact supported
protocol subset and known limitations. A successful binary build does not
certify every upstream Provider or protocol feature.


---

# 跨平台安装与 DeepSeek 网页配对（2026-11-09 更新）

> 本节是当前开发分支的操作指南。当前 dev 代码已包含 DeepSeek Web 配对扩展和 Provider 运行时；但真实 DeepSeek 在线推理仍须通过配对、流式回复、第二轮连续性、重启恢复和撤销授权验收。CI 通过不等于在线验收通过。尚未发布正式 Release 时，不要假定存在可下载的 v1.0.0 安装包。

## 一、平台支持矩阵

| 平台 | 网关运行 | DeepSeek 网页扩展配对 | 说明 |
|---|---|---|---|
| Windows 10/11 x64 | 原生 Windows 二进制 | 支持 Chrome / Edge | 建议网关和浏览器在同一台电脑 |
| Linux x64 / ARM64 | 原生 Linux 二进制 | 支持桌面 Chrome / Chromium | 无桌面服务器不能直接加载扩展 |
| macOS Intel / Apple Silicon | 原生 macOS 二进制 | 支持 Chrome / Chromium | 网关与浏览器应在同一台 Mac |
| Android / Termux | Linux ARM64 候选二进制 | 当前不支持 Android 浏览器配对 | 可运行普通网关/API；Termux 真机验收尚未完成 |
| Android 作为客户端 | 可访问另一台设备的 API | 不适用 | 需管理员主动配置可信 LAN/VPN 访问 |

**重要拓扑限制：** 当前 DeepSeek Web Connector 只允许向扩展所在设备的 127.0.0.1 / localhost 提交一次性配对数据；服务端同时校验 loopback 对端和精确扩展 Origin。因此，桌面扩展必须与 OmniRouter 网关运行在同一台设备。不要把扩展网关地址改成公网/LAN 地址，也不要放宽 loopback 或 Origin 校验。

## 二、通用准备与密钥

从源码构建需要 Go 1.23+、Node.js 22+、npm 和 Git。Vue 管理端只在构建期需要 Node.js；预编译二进制已嵌入管理静态资源，运行时不需要 Node.js、Python 或 Java。

    git clone https://github.com/qingjian0/OmniRouter.git
    cd OmniRouter
    git switch dev
    cd web
    npm install --no-audit --no-fund
    npm run build
    cd ..
    go test ./...
    go build -trimpath -o omnirouter ./cmd/omnirouter

Windows PowerShell 最后一条命令改为：

    go build -trimpath -o omnirouter.exe ./cmd/omnirouter

首次部署生成两种独立密钥：

    openssl rand -hex 32
    openssl rand -base64 32

第一条用于 OMNI_GATEWAY_API_KEY；第二条用于 OMNI_CREDENTIAL_ENCRYPTION_KEY（Base64 编码的 32 字节密钥）。首次生成后必须安全保存，后续启动和升级复用同一值。不要每次启动重新生成，也不要把密钥提交到 Git、截图或聊天中。丢失凭据加密密钥会导致数据库中既有密文无法解密。

常用配置：
- OMNI_ADDR：默认 127.0.0.1:8080，仅本机监听。
- OMNI_DATABASE_PATH：默认 data/omnirouter.db。
- OMNI_GATEWAY_API_KEY：保护 /v1/* 和 /admin/* 的 Bearer Key。
- OMNI_CREDENTIAL_ENCRYPTION_KEY：AES-GCM 凭据加密密钥。
- OMNI_DEEPSEEK_WEB_EXTENSION_ORIGIN：扩展精确 Origin，例如 chrome-extension://扩展ID；不可使用通配符。
- OMNI_PROVIDER_KIND=deepseek：这是 DeepSeek 官方 API Provider，不是 DeepSeek 网页登录态 Provider。

## 三、Windows 10 / 11

### 3.1 安装程序

正式发行后，下载 Release 页面中 windows-amd64 ZIP，解压到例如 C:\OmniRouter。当前尚无正式 Release 时，可按“通用准备与密钥”一节从源码构建。不要下载 Linux 二进制在 Windows 上运行。

### 3.2 PowerShell 配置与启动

在程序目录打开 PowerShell：

    New-Item -ItemType Directory -Force .\data | Out-Null
    $env:OMNI_ADDR = "127.0.0.1:8080"
    $env:OMNI_DATABASE_PATH = ".\data\omnirouter.db"
    $env:OMNI_GATEWAY_API_KEY = "<首次生成并安全保存的网关密钥>"
    $env:OMNI_CREDENTIAL_ENCRYPTION_KEY = "<首次生成并安全保存的 Base64 密钥>"
    .\omnirouter.exe

如需使用 DeepSeek 官方 API，再设置：

    $env:OMNI_PROVIDER_KIND = "deepseek"
    $env:OMNI_PROVIDER_ID = "deepseek"
    $env:OMNI_PROVIDER_NAME = "DeepSeek Official API"
    $env:OMNI_PROVIDER_BASE_URL = "https://api.deepseek.com"
    $env:OMNI_PROVIDER_API_KEY = "<官方 API Key>"
    $env:OMNI_PROVIDER_MODELS = "deepseek-flash,deepseek-v4-pro"

以上 $env 变量只在当前 PowerShell 窗口有效。关闭窗口会停止前台服务。长期运行请使用 Windows 服务管理器或受控服务环境变量注入，不要把真实密钥写入脚本或仓库。

### 3.3 验证

另开 PowerShell 窗口：

    Invoke-RestMethod http://127.0.0.1:8080/healthz
    Invoke-RestMethod http://127.0.0.1:8080/readyz
    $headers = @{ Authorization = "Bearer <你的网关Key>" }
    Invoke-RestMethod http://127.0.0.1:8080/v1/models -Headers $headers

浏览器访问 http://127.0.0.1:8080/ 打开管理控制台。

### 3.4 Chrome / Edge 扩展安装

1. Chrome 打开 chrome://extensions；Edge 打开 edge://extensions。
2. 打开“开发者模式”。
3. 点击“加载已解压的扩展程序”，选择 OmniRouter 目录下的 browser-extension 文件夹。
4. 在扩展详情页复制扩展 ID，扩展 Origin 是 chrome-extension://扩展ID。
5. 停止 OmniRouter；设置 OMNI_DEEPSEEK_WEB_EXTENSION_ORIGIN 为该精确 Origin 后重新启动。
6. 打开 https://chat.deepseek.com 并正常登录。
7. 在 OmniRouter 管理端确认 DeepSeek Web Provider 已配置，并启用明确的文本模型 allowlist。
8. 按“第七节：DeepSeek 网页会话配对”生成一次性码并在扩展弹窗中主动授权。

扩展重装、切换浏览器配置文件或扩展 ID 变化后，需要重新设置精确 Origin 并重启服务。

## 四、Linux

### 4.1 安装

按机器架构选择 linux-amd64 或 linux-arm64 发行包。解压后进入目录并赋予执行权限：

    tar -xzf omnirouter-<version>-linux-<arch>.tar.gz
    cd omnirouter-<version>-linux-<arch>
    chmod +x ./omnirouter

没有正式 Release 时按通用准备一节从源码构建。确认下载架构与 uname -m 一致。

### 4.2 配置与启动

    mkdir -p data
    export OMNI_ADDR=127.0.0.1:8080
    export OMNI_DATABASE_PATH=./data/omnirouter.db
    export OMNI_GATEWAY_API_KEY="<持久保存的网关密钥>"
    export OMNI_CREDENTIAL_ENCRYPTION_KEY="<持久保存的 Base64 加密密钥>"
    ./omnirouter

不要在每次启动时重新生成密钥。生产环境建议通过 systemd 的受保护 EnvironmentFile 或 Secret 管理机制注入。含密钥配置文件应限制权限，例如 chmod 600，且不得提交 Git。

### 4.3 验证

    curl -fsS http://127.0.0.1:8080/healthz
    curl -fsS http://127.0.0.1:8080/readyz
    curl -fsS http://127.0.0.1:8080/v1/models -H "Authorization: Bearer $OMNI_GATEWAY_API_KEY"

桌面 Linux 的 Chrome / Chromium 扩展安装方式与 Windows 相同：打开 chrome://extensions，开启开发者模式，加载 browser-extension 目录，复制扩展 ID，设置精确 Origin 并重启服务。无桌面环境的 Linux 服务器不能直接加载扩展；当前不支持由另一台电脑的浏览器直接向该服务器完成配对。

## 五、macOS（Intel / Apple Silicon）

### 5.1 选择架构并解压

- Intel Mac 使用 darwin-amd64。
- Apple Silicon 使用 darwin-arm64。

解压发行包后，在 Terminal 进入目录：

    chmod +x ./omnirouter
    mkdir -p data

如果 macOS 阻止首次运行，先核对下载来源和 SHA256 校验值，再查看“系统设置 → 隐私与安全性”的拦截提示。不要为了运行来源不明的程序而关闭 Gatekeeper。

### 5.2 配置与启动

    export OMNI_ADDR=127.0.0.1:8080
    export OMNI_DATABASE_PATH=./data/omnirouter.db
    export OMNI_GATEWAY_API_KEY="<持久保存的网关密钥>"
    export OMNI_CREDENTIAL_ENCRYPTION_KEY="<持久保存的 Base64 加密密钥>"
    ./omnirouter

如需官方 DeepSeek API，额外设置 OMNI_PROVIDER_KIND=deepseek、OMNI_PROVIDER_BASE_URL=https://api.deepseek.com 和官方 API Key。不要把网页登录 Cookie 当作官方 API Key。

### 5.3 Chrome 扩展

在同一台 Mac 的 Chrome 打开 chrome://extensions，开启开发者模式，加载 browser-extension 目录并复制扩展 ID。设置 OMNI_DEEPSEEK_WEB_EXTENSION_ORIGIN=chrome-extension://扩展ID，重启 OmniRouter。打开 DeepSeek 网页正常登录，再按第七节配对。

## 六、Android / Termux（网关运行，不是 APK）

**状态：实验性候选路径，尚未完成 Android 真机发布验收。** Linux ARM64 构建不是 Android APK；必须在目标手机的 Termux 环境实测，不能仅凭 Linux ARM64 交叉编译成功就宣称 Android 已正式支持。

### 6.1 安装 Termux

建议从 F-Droid 或 Termux 官方 GitHub Releases 安装 Termux，不要混装不同签名来源的 Termux 和插件。打开 Termux：

    pkg update && pkg upgrade
    pkg install curl tar openssl

### 6.2 确认架构并获取程序

    uname -m

常见 64 位 ARM 手机应显示 aarch64。解压 linux-arm64 发行包：

    tar -xzf omnirouter-<version>-linux-arm64.tar.gz
    cd omnirouter-<version>-linux-arm64
    chmod +x ./omnirouter

不要在 ARM64 手机上运行 amd64 文件。若出现 Exec format error，先检查 CPU 架构和下载资产。

### 6.3 首次启动与验证

    mkdir -p "$HOME/omnirouter/data"
    export OMNI_ADDR=127.0.0.1:8080
    export OMNI_DATABASE_PATH="$HOME/omnirouter/data/omnirouter.db"
    export OMNI_GATEWAY_API_KEY="<首次生成并安全保存的网关密钥>"
    export OMNI_CREDENTIAL_ENCRYPTION_KEY="<首次生成并安全保存的 Base64 密钥>"
    ./omnirouter

另开 Termux 会话执行：

    curl -fsS http://127.0.0.1:8080/healthz
    curl -fsS http://127.0.0.1:8080/readyz
    bash scripts/termux-smoke.sh ./omnirouter

数据库建议放在 Termux 私有 HOME 目录，不要放在受 Android 存储权限限制的共享目录。Android 可能限制后台进程，长时间运行需实机检查电池优化、后台限制、进程重启和 SQLite 持久化。

### 6.4 Android 浏览器与配对限制

- Android Chrome 不支持本项目当前的桌面 Chromium MV3 扩展安装流程。
- Kiwi、其他第三方浏览器或 Firefox 的扩展兼容性没有经过本项目验收，不应视为官方支持。
- Termux 网关可以提供普通本机 API；但当前 DeepSeek Web Connector 需要在扩展所在设备访问 loopback 网关，因此另一台电脑上的扩展不能直接配对 Android Termux 上的网关。
- Android 可作为 API 客户端访问另一台电脑的 OmniRouter，但需要管理员自行配置可信 LAN/VPN、TLS、防火墙和 API Key。不要直接把管理端口暴露到公网。
- Android 真机验收还需记录 Android 版本、设备架构、Termux 版本、二进制 SHA256，并测试启动、SQLite 读写、重启恢复、存储权限、后台运行和正常退出。

详见 docs/ANDROID-TERMUX-ACCEPTANCE.md。

## 七、DeepSeek 网页会话配对（Windows / Linux / macOS）

### 7.1 前置条件

1. OmniRouter 与 Chrome / Chromium / Edge 在同一台电脑运行。
2. OMNI_GATEWAY_API_KEY 已配置。
3. OMNI_CREDENTIAL_ENCRYPTION_KEY 已配置，且跨重启保持不变。
4. browser-extension 已加载，OMNI_DEEPSEEK_WEB_EXTENSION_ORIGIN 已设置为精确扩展 Origin。
5. 管理端已有 DeepSeek Web Provider，且至少启用一个明确的文本模型。
6. 浏览器已正常登录 https://chat.deepseek.com。

### 7.2 生成一次性配对码

在网关所在电脑执行，将 Provider ID 替换为管理端实际 ID：

    curl -X POST http://127.0.0.1:8080/admin/deepseek-web/pairings \
      -H "Authorization: Bearer $OMNI_GATEWAY_API_KEY" \
      -H "Content-Type: application/json" \
      -d '{"provider_id":"<deepseek-web-provider-id>","account_name":"My DeepSeek Web account"}'

响应中的 nonce 是一次性配对码，约两分钟过期且只能使用一次。不要分享 nonce、Cookie、Token、浏览器配置文件或 HAR。

### 7.3 扩展授权

1. 打开已登录的 DeepSeek 网页标签页。
2. 点击浏览器工具栏中的 OmniRouter DeepSeek Web Connector。
3. 网关地址保持 http://127.0.0.1:8080。
4. 将刚生成的 nonce 粘贴到配对码输入框。
5. 点击 Connect this DeepSeek tab，等待成功状态。
6. 失败或过期时重新生成 nonce；旧码不能重复消费。

扩展只在用户主动点击后读取当前活动 DeepSeek 标签页所需授权字段；不会读取密码、MFA、聊天记录或其他标签页。配对成功只证明授权材料已存入本地加密凭据库，不证明 DeepSeek 当前在线接口接受该会话。

## 八、API 基础验证

    curl http://127.0.0.1:8080/v1/models \
      -H "Authorization: Bearer $OMNI_GATEWAY_API_KEY"

流式 Chat Completions 示例：

    curl http://127.0.0.1:8080/v1/chat/completions \
      -H "Authorization: Bearer $OMNI_GATEWAY_API_KEY" \
      -H "Content-Type: application/json" \
      -H "X-OmniRouter-Conversation-ID: local-test-001" \
      -d '{"model":"<enabled-model-id>","stream":true,"messages":[{"role":"user","content":"Reply with a short greeting."}]}'

要验证多轮连续性，每轮请求都必须复用相同的 X-OmniRouter-Conversation-ID。该值只是本地会话键，不是 DeepSeek 上游 session ID、Cookie 或凭据。

## 九、常见问题

| 问题 | 检查方式 |
|---|---|
| connection refused | 确认进程运行、端口正确且本机防火墙未拦截 |
| API 返回 401 | 检查 Bearer Key 是否与运行进程的 OMNI_GATEWAY_API_KEY 一致 |
| 重启后凭据无法解密 | 确认没有更换或丢失 OMNI_CREDENTIAL_ENCRYPTION_KEY |
| 配对 Origin 错误 | 检查扩展 ID、精确 Origin 环境变量，并在修改后重启网关 |
| 配对码失效 | 重新生成；nonce 过期或消费后不可复用 |
| 扩展找不到 DeepSeek 标签页 | 当前活动标签必须是 https://chat.deepseek.com |
| 配对成功但模型不可用 | 检查 Provider 状态、模型 allowlist 和账户状态 |
| 上游返回 401 / 403 / 429 | 检查授权是否过期、上游策略限制或限流；按正常流程重新授权或等待，不绕过验证 |
| Android 无法安装扩展 | 当前 Android 扩展配对未支持；可将 Android 作为普通 API 客户端 |

## 十、升级、备份与安全

- 默认监听 127.0.0.1:8080，不要直接暴露公网。
- SQLite 默认位于 data/omnirouter.db，确保目录可写并定期备份。
- 数据库备份必须与凭据加密密钥分开安全保存；恢复时两者缺一不可。
- 升级前停止服务并备份数据库，替换二进制后复用原密钥。
- 不要在日志、工单或聊天中粘贴 Cookie、Token、API Key、nonce 或完整 HAR。
- CI 通过只证明构建与自动化测试通过；DeepSeek Web 最终验收还必须完成真实配对、流式回复、第二轮连续性、重启恢复和撤销授权。
