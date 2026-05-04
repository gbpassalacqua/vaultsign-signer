// content-script.js — relays window.postMessage requests from the page
// to the background service worker, then echoes the response back.
//
// Trust boundary: only messages that originate from the same window
// AND carry the protocol-specific type field are forwarded. Pages outside
// the manifest's content_scripts.matches glob never run this script.

(function () {
  const REQUEST_TYPE = "VAULTSIGN_SIGNER_REQUEST";
  const RESPONSE_TYPE = "VAULTSIGN_SIGNER_RESPONSE";

  window.addEventListener("message", (event) => {
    if (event.source !== window) return;
    const data = event.data;
    if (!data || typeof data !== "object" || data.type !== REQUEST_TYPE) return;

    // Round-trip the requestId so the page can correlate concurrent calls.
    const requestId = data.requestId;

    chrome.runtime.sendMessage(data, (response) => {
      const lastErr = chrome.runtime.lastError;
      const payload = {
        type: RESPONSE_TYPE,
        requestId,
      };
      if (lastErr) {
        payload.error = lastErr.message || "extension messaging failed";
      } else if (!response) {
        payload.error = "empty response from background";
      } else {
        Object.assign(payload, response);
      }
      window.postMessage(payload, "*");
    });
  });
})();
