// background.js — service worker that bridges chrome.runtime messages to
// the vaultsign-host native binary over a single Native Messaging port.
//
// MV3 service workers can hibernate, but an open port keeps the worker
// alive. We connect lazily on the first request and reconnect on every
// disconnect. Requests to the native host are serialized through a FIFO
// queue: the host responds in the order requests arrive, so we just
// match the next-pending callback to each incoming response.

const HOST_NAME = "com.vaultsign.signer";

let port = null;
const pending = []; // FIFO of { resolve, reject } callbacks

function connect() {
  console.log("[vaultsign-bg] connecting to native host:", HOST_NAME);
  port = chrome.runtime.connectNative(HOST_NAME);

  port.onMessage.addListener((msg) => {
    console.log("[vaultsign-bg] <- host:", summarize(msg));
    const cb = pending.shift();
    if (cb) cb.resolve(msg);
    else console.warn("[vaultsign-bg] orphan response (no pending request):", msg);
  });

  port.onDisconnect.addListener(() => {
    const err = chrome.runtime.lastError;
    const message = err && err.message ? err.message : "(no error message)";
    console.error("[vaultsign-bg] native host disconnected:", message);
    while (pending.length) {
      const cb = pending.shift();
      cb.reject(new Error("native host disconnected: " + message));
    }
    port = null;
  });
}

function nativeRequest(payload) {
  return new Promise((resolve, reject) => {
    if (!port) {
      try { connect(); }
      catch (e) { reject(e); return; }
    }
    pending.push({ resolve, reject });
    try {
      console.log("[vaultsign-bg] -> host:", summarize(payload));
      port.postMessage(payload);
    } catch (e) {
      // Pop the just-pushed callback — the post failed before reaching the host.
      pending.pop();
      reject(e);
    }
  });
}

chrome.runtime.onMessage.addListener((message, sender, sendResponse) => {
  // Only relay requests that match our protocol shape. Strip transport-only
  // fields (type, requestId) before forwarding to the host — the page-level
  // requestId is round-tripped by the content script, not the host.
  if (!message || message.type !== "VAULTSIGN_SIGNER_REQUEST") return false;

  const { type, requestId, ...payload } = message;
  nativeRequest(payload)
    .then((resp) => sendResponse(resp))
    .catch((err) => sendResponse({ error: err.message || String(err) }));

  return true; // keep the message channel open for the async sendResponse
});

function summarize(obj) {
  // Avoid logging giant base64 blobs in the console.
  const out = {};
  for (const [k, v] of Object.entries(obj)) {
    if (typeof v === "string" && v.length > 80) {
      out[k] = `${v.slice(0, 32)}…(${v.length} chars)`;
    } else {
      out[k] = v;
    }
  }
  return out;
}
