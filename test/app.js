// app.js — talks to the VaultSign Signer extension via window.postMessage.
//
// Each request gets a UUID; the response carries the same requestId so
// concurrent calls don't cross wires. The extension's content-script picks
// up the message, hands it to the background service worker, which talks
// to the native Go binary over Chrome Native Messaging stdio.

const REQUEST_TYPE = "VAULTSIGN_SIGNER_REQUEST";
const RESPONSE_TYPE = "VAULTSIGN_SIGNER_RESPONSE";
const TIMEOUT_MS = 15000;

function uuid() {
  if (window.crypto && crypto.randomUUID) return crypto.randomUUID();
  return Date.now().toString(36) + Math.random().toString(36).slice(2);
}

function send(action, extra = {}) {
  return new Promise((resolve, reject) => {
    const requestId = uuid();
    const handler = (event) => {
      if (event.source !== window) return;
      const d = event.data;
      if (!d || d.type !== RESPONSE_TYPE || d.requestId !== requestId) return;
      cleanup();
      resolve(d);
    };
    const timer = setTimeout(() => {
      cleanup();
      reject(new Error(`timeout after ${TIMEOUT_MS}ms — extension installed?`));
    }, TIMEOUT_MS);
    function cleanup() {
      window.removeEventListener("message", handler);
      clearTimeout(timer);
    }
    window.addEventListener("message", handler);
    window.postMessage({ type: REQUEST_TYPE, action, requestId, ...extra }, "*");
  });
}

async function sha256Base64(text) {
  const buf = new TextEncoder().encode(text);
  const hash = await crypto.subtle.digest("SHA-256", buf);
  const bytes = new Uint8Array(hash);
  let bin = "";
  for (let i = 0; i < bytes.length; i++) bin += String.fromCharCode(bytes[i]);
  return btoa(bin);
}

const out = document.getElementById("out");
const status = document.getElementById("status");

function show(obj, isErr = false) {
  out.textContent = typeof obj === "string" ? obj : JSON.stringify(obj, null, 2);
  out.className = isErr ? "err" : "";
}

function setStatus(text, kind) {
  status.textContent = text;
  status.className = kind || "";
}

async function detect() {
  setStatus("extensão: detectando…");
  try {
    const r = await send("ping");
    if (r.error) {
      setStatus(`extensão: erro — ${r.error}`, "err");
      return;
    }
    if (r.action === "pong") {
      setStatus(`extensão conectada — native host v${r.version}`, "ok");
    } else {
      setStatus(`extensão: resposta inesperada — ${JSON.stringify(r)}`, "err");
    }
  } catch (e) {
    setStatus(`extensão: não detectada (${e.message})`, "err");
  }
}

document.getElementById("ping").addEventListener("click", async () => {
  show("(pinging…)");
  try { show(await send("ping")); }
  catch (e) { show(e.message, true); }
});

document.getElementById("list").addEventListener("click", async () => {
  show("(listing certificates…)");
  try {
    const r = await send("listCertificates");
    show(r, !!r.error);
  } catch (e) {
    show(e.message, true);
  }
});

document.getElementById("sign").addEventListener("click", async () => {
  const thumbprint = document.getElementById("thumbprint").value.trim();
  if (!/^[0-9A-Fa-f]{40}$/.test(thumbprint)) {
    show("thumbprint deve ser SHA-1 hex de 40 chars", true);
    return;
  }
  show("(signing… autorize o acesso à chave se o Windows pedir)");
  try {
    const text = "VaultSign teste";
    const hash = await sha256Base64(text);
    const r = await send("signHash", {
      thumbprint: thumbprint.toUpperCase(),
      hash,
      hashAlgorithm: "SHA-256",
    });
    if (r.error) {
      show(r, true);
      return;
    }
    show({
      action: r.action,
      hashed_text: text,
      hash_b64: hash,
      pkcs7_size_bytes: r.signature ? Math.floor(r.signature.length * 3 / 4) : 0,
      certificate_size_bytes: r.certificate ? Math.floor(r.certificate.length * 3 / 4) : 0,
      pkcs7_b64_preview: r.signature ? r.signature.slice(0, 80) + "…" : null,
      pkcs7_b64_full: r.signature,
      certificate_b64: r.certificate,
    });
  } catch (e) {
    show(e.message, true);
  }
});

detect();
