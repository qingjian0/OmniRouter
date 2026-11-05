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
$env:OMNI_PROVIDER_ID = "deepseek"
$env:OMNI_PROVIDER_NAME = "DeepSeek"
$env:OMNI_PROVIDER_BASE_URL = "https://api.deepseek.com/v1"
$env:OMNI_PROVIDER_API_KEY = "<provider-api-key>"
$env:OMNI_PROVIDER_MODELS = "deepseek-chat,deepseek-reasoner"
$env:OMNI_PROVIDER_MODEL_CAPABILITIES = '{"deepseek-chat":["chat","streaming","tools"],"deepseek-reasoner":["chat","streaming"]}'
.\omnirouter.exe
```

## Verify the service

Check the embedded version and build commit before starting:

```sh
./omnirouter --version
```

On Windows use `.\\omnirouter.exe --version` in PowerShell. Then check the service:

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
- Anthropic Messages subset: `POST /v1/messages`
- Gemini subset: `POST /v1beta/models/{model}:generateContent` and
  `streamGenerateContent`
- Operations: `/healthz`, `/readyz`, and authenticated `/admin/*` endpoints

Consult the repository README and release notes for the exact supported
protocol subset and known limitations. A successful binary build does not
certify every upstream Provider or protocol feature.
