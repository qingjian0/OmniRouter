# OmniRouter DeepSeek Web Connector (Chrome / Edge)

This is an opt-in Manifest V3 extension. It does not sign in for you. Sign in to
DeepSeek normally in a browser tab, then click the extension and explicitly
press **Connect this DeepSeek tab**.

## Install for local testing

1. Start OmniRouter with a persistent credential encryption key and gateway API
   key configured.
2. Set `OMNI_DEEPSEEK_WEB_EXTENSION_ORIGIN` to the exact extension origin
   shown by Chrome at `chrome://extensions` (for example,
   `chrome-extension://<extension-id>`). Do not use a wildcard.
3. Restart OmniRouter after changing the environment.
4. Open `chrome://extensions`, enable Developer mode, choose **Load unpacked**,
   and select this directory.
5. Open `https://chat.deepseek.com` and sign in normally.
6. Create a pairing code through the authenticated admin API:

   ```sh
   curl -X POST http://127.0.0.1:8080/admin/deepseek-web/pairings \
     -H "Authorization: Bearer $OMNI_GATEWAY_API_KEY" \
     -H "Content-Type: application/json" \
     -d '{"provider_id":"<deepseek-web-provider-id>","account_name":"Personal DeepSeek Web"}'
   ```

7. Copy the returned `nonce` into the extension popup, confirm the local
   gateway address, and press **Connect this DeepSeek tab**.

The extension origin may change when the extension is installed in another
browser profile or rebuilt without a stable extension key. Update the exact
allowlist value and restart OmniRouter if that happens.

## Permissions and data boundary

- `activeTab` and `scripting`: inspect only the active tab after the user
  presses the extension action.
- `cookies` plus the exact DeepSeek host permission: read only secure cookies
  scoped to `chat.deepseek.com`.
- Localhost host permissions: submit the one-time payload to the local gateway.
- The extension reads only `localStorage.userToken` and the minimum cookie
  fields accepted by the gateway. It does not read passwords, MFA, page text,
  chat history, other tabs, or browser profile files.
- Token bytes are base64-encoded for JSON transport; base64 is not encryption.
  The connection is loopback-only and the gateway immediately stores secrets
  through its encrypted credential vault.
- The pairing code expires after two minutes and can be consumed only once.

This initial package targets Chromium Manifest V3. Firefox and Android browser
support are not claimed until separately tested.
