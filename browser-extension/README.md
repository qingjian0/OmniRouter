# OmniRouter DeepSeek Web Connector

This is an opt-in Chromium Manifest V3 extension. It does not sign in for you. Sign in to DeepSeek normally in a browser tab, then explicitly click **Connect this DeepSeek tab** in the extension popup.

## Supported desktop setup

- Windows: Google Chrome and Microsoft Edge.
- Linux: Google Chrome / Chromium with a graphical desktop session.
- macOS: Google Chrome / Chromium.

The OmniRouter gateway and browser extension must run on the **same computer**. The extension submits the one-time pairing payload only to the local loopback gateway (127.0.0.1 / localhost), and the gateway checks both loopback peer and exact extension Origin. Do not change the popup gateway address to a public or LAN address, and do not weaken those checks.

Android Chrome does not support this desktop extension installation workflow. Termux can run an experimental Linux ARM64 gateway binary, but Android browser-extension pairing is not implemented or certified. A desktop browser on another device cannot pair with an OmniRouter instance running inside Android Termux through the current loopback-only flow.

## Install and configure

For complete device-specific steps, including Windows PowerShell, Linux shell, macOS Terminal, Android/Termux runtime, key generation, verification, and troubleshooting, see [the cross-platform installation guide](../docs/INSTALL.md).

Short desktop checklist:

1. Start OmniRouter with a persistent credential encryption key and gateway API key configured.
2. Open chrome://extensions (Chrome/Chromium) or edge://extensions (Edge).
3. Enable Developer mode and choose **Load unpacked**.
4. Select this browser-extension directory.
5. Copy the extension ID shown by the browser.
6. Set OMNI_DEEPSEEK_WEB_EXTENSION_ORIGIN to the exact value chrome-extension://<extension-id>, then restart OmniRouter.
7. Open https://chat.deepseek.com and sign in normally.
8. Create a one-time pairing code through the authenticated admin API:

        curl -X POST http://127.0.0.1:8080/admin/deepseek-web/pairings -H 'Authorization: Bearer $OMNI_GATEWAY_API_KEY' -H 'Content-Type: application/json' -d '{"provider_id":"<deepseek-web-provider-id>","account_name":"Personal DeepSeek Web"}'

9. Copy the returned nonce into the extension popup, keep the gateway address at http://127.0.0.1:8080, and explicitly click **Connect this DeepSeek tab**.

The nonce expires after about two minutes and can be consumed once. If it expires or pairing fails, create a new nonce; do not reuse an old one.

## Permissions and data boundary

- activeTab and scripting: inspect only the active tab after the user presses the extension action.
- cookies plus the exact DeepSeek host permission: read the secure cookie fields scoped to chat.deepseek.com.
- Localhost host permissions: submit the one-time payload to the local gateway.
- The extension reads only localStorage.userToken and the cookie fields accepted by the gateway. It does not read passwords, MFA, page text, chat history, other tabs, or browser profile files.
- Token bytes are base64-encoded for JSON transport; base64 is not encryption. The connection is loopback-only and the gateway stores secrets through its encrypted credential vault.
- The pairing code is short-lived and single-use.

## Acceptance status

A successful pairing means only that user-authorized credential material was stored locally. It does not prove that DeepSeek currently accepts the session. End-to-end acceptance must still verify one live streamed completion, a second turn with continuity, restart restoration, and authorization revocation. Do not share raw tokens, cookies, pairing codes, browser profile data, or HAR files for troubleshooting; sanitized status and error categories are enough.