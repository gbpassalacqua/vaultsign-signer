// background.js — service worker that bridges chrome.runtime messages to
// the vaultsign-host native binary over a single Native Messaging port.
//
// MV3 service workers can hibernate, but an open port keeps the worker
// alive. We connect lazily on the first request and reconnect on every
// disconnect. Requests to the native host are serialized through a FIFO
// queue: the host responds in the order requests arrive, so we just
// match the next-pending callback to each incoming response.
//
// Two ingress paths share the same handler:
//   1. chrome.runtime.onMessage         — internal, used by the content
//      script injected into vaultsign.app pages on navigation.
//   2. chrome.runtime.onMessageExternal — external, used by SDK code
//      running directly on vaultsign.app pages via the
//      externally_connectable manifest entry. This path is essential when
//      the user installs the extension into an already-open tab — the
//      content script does not retroactively inject into existing pages,
//      but onMessageExternal works immediately on the next sendMessage call.

const HOST_NAME = "com.vaultsign.signer";
const REQUEST_TYPE = "VAULTSIGN_SIGNER_REQUEST";
const RESPONSE_TYPE = "VAULTSIGN_SIGNER_RESPONSE";

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

// Shared handler registered on both onMessage and onMessageExternal so
// content-script-routed messages and externally_connectable-routed messages
// take the same code path. The response is always wrapped with
// { type: VAULTSIGN_SIGNER_RESPONSE, requestId, ... } so SDK callers see a
// consistent shape regardless of how they reached us.
function handleRequest(message, sender, sendResponse) {
  if (!message || message.type !== REQUEST_TYPE) return false;

  const { type, requestId, ...payload } = message;

  nativeRequest(payload)
    .then((resp) => {
      sendResponse({
        type: RESPONSE_TYPE,
        requestId,
        ...resp,
      });
    })
    .catch((err) => {
      sendResponse({
        type: RESPONSE_TYPE,
        requestId,
        error: err.message || String(err),
      });
    });

  return true; // keep the message channel open for the async sendResponse
}

chrome.runtime.onMessage.addListener(handleRequest);
chrome.runtime.onMessageExternal.addListener(handleRequest);

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
