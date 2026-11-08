const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const vm = require("node:vm");
const path = require("node:path");

const source = fs.readFileSync(path.join(__dirname, "popup.js"), "utf8");

function harness(overrides = {}) {
  const calls = { tabs: 0, scripts: 0, cookies: 0, fetch: [] };
  const nodes = {
    gateway: { value: "http://127.0.0.1:8080" },
    nonce: { value: "a".repeat(64) },
    connect: {
      disabled: false,
      listeners: {},
      addEventListener(name, callback) { this.listeners[name] = callback; }
    },
    status: { textContent: "", dataset: {} }
  };
  const chrome = {
    tabs: {
      async query() {
        calls.tabs++;
        return [overrides.tab || { id: 7, url: "https://chat.deepseek.com/" }];
      }
    },
    scripting: {
      async executeScript(details) {
        calls.scripts++;
        calls.scriptDetails = details;
        return [{ result: overrides.token === undefined ? "user-token-测试" : overrides.token }];
      }
    },
    cookies: {
      async get(details) {
        calls.cookies++;
        calls.cookieDetails = details;
        return overrides.cookie === undefined
          ? { name: "ds_session_id", value: "session-value", domain: "chat.deepseek.com", path: "/", secure: true }
          : overrides.cookie;
      }
    }
  };
  const fetch = async (...args) => {
    calls.fetch.push(args);
    return overrides.response || { ok: true, status: 201, json: async () => ({ account_id: "account-123" }) };
  };
  vm.runInNewContext(source, {
    document: { getElementById: id => nodes[id] },
    chrome,
    fetch,
    URL,
    TextEncoder,
    btoa,
    Error
  }, { filename: "popup.js" });
  return { calls, nodes, click: () => nodes.connect.listeners.click() };
}

test("does not inspect tabs or credentials until the user clicks Connect", () => {
  const h = harness();
  assert.deepEqual(h.calls, { tabs: 0, scripts: 0, cookies: 0, fetch: [] });
});

test("pairs only after click and sends the minimum expected payload to loopback", async () => {
  const h = harness();
  await h.click();

  assert.equal(h.calls.tabs, 1);
  assert.equal(h.calls.scripts, 1);
  assert.equal(h.calls.cookies, 1);
  assert.equal(h.calls.scriptDetails.target.tabId, 7);
  assert.equal(JSON.stringify(h.calls.cookieDetails), JSON.stringify({ url: "https://chat.deepseek.com/", name: "ds_session_id" }));
  assert.equal(h.calls.fetch.length, 1);

  const [url, options] = h.calls.fetch[0];
  assert.equal(url, "http://127.0.0.1:8080/internal/deepseek-web/pairings");
  assert.equal(options.method, "POST");
  assert.equal(options.credentials, "omit");
  assert.equal(options.cache, "no-store");
  assert.equal(options.headers["X-Omni-Pairing-Nonce"], "a".repeat(64));
  const payload = JSON.parse(options.body);
  assert.equal(Buffer.from(payload.token, "base64").toString("utf8"), "user-token-测试");
  assert.equal(JSON.stringify(payload.cookies), JSON.stringify([{
    name: "ds_session_id",
    value: "session-value",
    domain: "chat.deepseek.com",
    path: "/",
    secure: true
  }]));
  assert.equal(h.nodes.nonce.value, "");
  assert.equal(h.nodes.status.dataset.state, "success");
  assert.equal(h.nodes.connect.disabled, false);
});

test("rejects a non-DeepSeek active tab before reading session values", async () => {
  const h = harness({ tab: { id: 9, url: "https://example.com/" } });
  await h.click();

  assert.equal(h.calls.tabs, 1);
  assert.equal(h.calls.scripts, 0);
  assert.equal(h.calls.cookies, 0);
  assert.equal(h.calls.fetch.length, 0);
  assert.equal(h.nodes.status.dataset.state, "error");
});

test("rejects a missing or insecure session cookie without sending credentials", async () => {
  const h = harness({
    cookie: { name: "ds_session_id", value: "session-value", domain: "chat.deepseek.com", path: "/", secure: false }
  });
  await h.click();

  assert.equal(h.calls.scripts, 1);
  assert.equal(h.calls.cookies, 1);
  assert.equal(h.calls.fetch.length, 0);
  assert.equal(h.nodes.status.dataset.state, "error");
  assert.match(h.nodes.status.textContent, /session cookie was not found/);
});

test("rejects non-loopback gateway URLs before inspecting the active tab", async () => {
  const h = harness();
  h.nodes.gateway.value = "http://192.168.1.10:8080";
  await h.click();

  assert.equal(h.calls.tabs, 0);
  assert.equal(h.calls.scripts, 0);
  assert.equal(h.calls.cookies, 0);
  assert.equal(h.calls.fetch.length, 0);
  assert.equal(h.nodes.status.dataset.state, "error");
});

test("does not clear the pairing code or claim success when the gateway rejects the request", async () => {
  const h = harness({ response: { ok: false, status: 403 } });
  await h.click();

  assert.equal(h.calls.fetch.length, 1);
  assert.equal(h.nodes.nonce.value, "a".repeat(64));
  assert.equal(h.nodes.status.dataset.state, "error");
  assert.match(h.nodes.status.textContent, /HTTP 403/);
});
