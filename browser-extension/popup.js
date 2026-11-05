const gatewayInput = document.getElementById("gateway");
const nonceInput = document.getElementById("nonce");
const connectButton = document.getElementById("connect");
const statusNode = document.getElementById("status");

function setStatus(message, state = "") {
  statusNode.textContent = message;
  statusNode.dataset.state = state;
}

function encodeBase64UTF8(value) {
  const bytes = new TextEncoder().encode(value);
  let binary = "";
  for (const byte of bytes) binary += String.fromCharCode(byte);
  return btoa(binary);
}

function parseLocalGateway(raw) {
  const url = new URL(raw);
  if (
    url.protocol !== "http:" ||
    !["127.0.0.1", "localhost"].includes(url.hostname) ||
    url.username || url.password || url.pathname !== "/" ||
    url.search || url.hash
  ) {
    throw new Error("Use a local address such as http://127.0.0.1:8080");
  }
  return url.origin;
}

connectButton.addEventListener("click", async () => {
  let token = "";
  let cookies = [];
  let payload = null;
  connectButton.disabled = true;
  setStatus("Checking the active tab…");
  try {
    const gateway = parseLocalGateway(gatewayInput.value.trim());
    const nonce = nonceInput.value.trim();
    if (!/^[a-f0-9]{64}$/i.test(nonce)) {
      throw new Error("Enter the 64-character one-time pairing code from OmniRouter.");
    }

    const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
    if (!tab?.id || !tab.url || new URL(tab.url).origin !== "https://chat.deepseek.com") {
      throw new Error("Open https://chat.deepseek.com in the active tab first.");
    }

    setStatus("Reading the minimum session values after your explicit click…");
    const injected = await chrome.scripting.executeScript({
      target: { tabId: tab.id },
      func: () => window.localStorage.getItem("userToken")
    });
    token = injected?.[0]?.result || "";
    if (!token) throw new Error("No userToken was found. Sign in to DeepSeek normally, then retry.");

    const browserCookies = await chrome.cookies.getAll({ url: "https://chat.deepseek.com/" });
    cookies = browserCookies
      .filter(cookie => {
        const domain = cookie.domain.replace(/^\./, "").toLowerCase();
        return domain === "chat.deepseek.com" && cookie.secure &&
          (cookie.path === "/" || cookie.path === "");
      })
      .map(cookie => ({
        name: cookie.name,
        value: cookie.value,
        domain: cookie.domain,
        path: cookie.path,
        secure: cookie.secure
      }));
    if (!cookies.some(cookie => cookie.name === "ds_session_id" && cookie.value)) {
      throw new Error("The required DeepSeek session cookie was not found. Sign in again and retry.");
    }

    payload = { token: encodeBase64UTF8(token), cookies };
    setStatus("Sending the one-time authorization to local OmniRouter…");
    const response = await fetch(
      gateway + "/internal/deepseek-web/pairings/" + encodeURIComponent(nonce),
      {
        method: "POST",
        mode: "cors",
        credentials: "omit",
        cache: "no-store",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload)
      }
    );
    if (!response.ok) {
      throw new Error("OmniRouter rejected the pairing (HTTP " + response.status + "). Create a new pairing code if it expired.");
    }
    const result = await response.json();
    nonceInput.value = "";
    setStatus("Connected. Account " + result.account_id + " was stored in the encrypted local vault.", "success");
  } catch (error) {
    setStatus(error instanceof Error ? error.message : "Pairing failed.", "error");
  } finally {
    token = "";
    cookies = [];
    payload = null;
    connectButton.disabled = false;
  }
});
