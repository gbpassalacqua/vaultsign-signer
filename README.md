# VaultSign Signer

Chrome extension + Go native host that exposes Brazilian **ICP-Brasil**
digital certificates (installed on the user's Windows machine, accessed
via Windows CryptoAPI / CNG) to a Chromium-based browser through the
Native Messaging API.

The host returns a detached CMS / PKCS#7 SignedData over a 32-byte
SHA-256 digest supplied by the caller. The private key **never leaves
the user's machine** — only the digest crosses the wire to the host,
and the resulting PKCS#7 (~3 KB) crosses back.

[vaultsign.app](https://vaultsign.app) is the reference integration,
but the wire protocol is documented below and any web application (or
any other Chromium extension) can use the host. The host knows nothing
about VaultSign specifically — it speaks a generic
*"give me a list of ICP-Brasil certs / sign this hash with cert X"*
protocol.

## Status

**v0.1.1 released — 2026-05-08.** Chrome Web Store listing live.
Working end-to-end on Windows: page → content script → background
service worker → native host → Windows CryptoAPI → PKCS#7 back.

Roadmap (not started):
- macOS / Linux native host
- Public installer (`/install` site + `.msi` / Inno Setup wizard)
- A3 token (smart card / USB token) — currently only A1 (cert imported
  into `CurrentUser\My`) is tested end-to-end

## Why this exists

Brazilian electronic-signature applications that target ICP-Brasil
qualified signatures (validable at [validar.iti.gov.br](https://validar.iti.gov.br))
need access to the user's local certificate store. Browsers don't
expose CryptoAPI directly, so the canonical pattern is:

```
browser → extension → Native Messaging → native helper → Windows cert store
```

This repo is that helper, plus the extension that bridges the page to
it. Both are MIT-licensed and reusable by anyone building Brazilian
e-signature software.

## Prerequisites

- Windows 10 / 11.
- An ICP-Brasil A1 certificate (`.pfx`) imported into the user's
  `Cert:\CurrentUser\My` store. A3 (token) is on the roadmap.
- Go 1.23+ (build).
- Google Chrome, Edge, or any Chromium ≥ MV3.

## Build

```powershell
cd host
go build -o vaultsign-host.exe ./cmd/vaultsign-host
go build -o framer.exe        ./cmd/framer
go build -o extkey.exe        ./cmd/extkey       # one-time: extension key + ID
go build -o icongen.exe       ./cmd/icongen      # one-time: placeholder icons
go build -o devserver.exe     ./cmd/devserver    # serves test/ on :8080
```

CI builds run on `windows-latest` GitHub Actions and produce a signed
`vaultsign-host.exe` — see [`.github/workflows/build-sign-release.yml`](.github/workflows/build-sign-release.yml).

### Smart App Control (Win11)

Smart App Control on Windows 11 blocks unsigned binaries that touch
`crypt32.dll` at exec. Earlier versions of SAC tolerated self-signed
binaries; recent updates check the certificate trust chain and reject
self-signed. Two paths today:

1. **Production-signed binary.** The CI workflow signs the host with a
   Microsoft-trusted cert (Microsoft Trusted Signing or EV code
   signing). SAC accepts these without user action. End-users get a
   signed installer.
2. **Self-sign the dev build (developer machines only).** Useful when
   iterating locally; works only if SAC is off or in evaluation mode.

   ```powershell
   # One-time: create the dev cert
   $cert = New-SelfSignedCertificate -Type CodeSigningCert `
     -Subject "CN=VaultSign Dev" `
     -CertStoreLocation Cert:\CurrentUser\My `
     -KeyUsage DigitalSignature -KeyAlgorithm RSA -KeyLength 2048 `
     -NotAfter (Get-Date).AddYears(2)

   # After every rebuild — SAC re-evaluates the binary on each launch
   Set-AuthenticodeSignature -FilePath .\vaultsign-host.exe -Certificate $cert
   ```

3. **Disable Smart App Control entirely** (one-way — can't be re-enabled
   without reinstalling Windows): Settings → Windows Security → App &
   browser control → Smart App Control settings → Off → confirm in the
   warning dialog → reboot. Verify with
   `reg query "HKLM\SYSTEM\CurrentControlSet\Control\CI\Policy" /v VerifiedAndReputablePolicyState`
   showing `0x0`.

## Standalone test (no extension required)

```powershell
# Ping
.\framer.exe encode '{"action":"ping"}' | .\vaultsign-host.exe | .\framer.exe decode

# List certificates
.\framer.exe encode '{"action":"listCertificates"}' | .\vaultsign-host.exe | .\framer.exe decode
```

## Setup pós-build no Windows (extensão + native host)

The Native Messaging protocol requires three things wired together: the
extension installed in Chrome, the host manifest on disk, and a registry
entry pointing Chrome at the manifest.

### 1. (Dev only) Self-sign the host binary

Skip this step if your binary is already production-signed by CI. The
included `host/scripts/install-native-host.ps1` detects an existing
valid signature and skips re-signing automatically.

```powershell
$cert = Get-ChildItem Cert:\CurrentUser\My | Where-Object { $_.Subject -eq 'CN=VaultSign Dev' } | Select-Object -First 1
Set-AuthenticodeSignature -FilePath .\host\vaultsign-host.exe -Certificate $cert
```

### 2. Register the Native Host in HKCU

> **Important:** the registry write must be done from a **native cmd or
> PowerShell window** (Win+R → cmd) — not from inside an automation tool
> that might run sandboxed. Some agent runtimes redirect HKCU writes to a
> per-process virtual hive that Chrome cannot see, even though `reg query`
> from inside the same context shows the key. The symptom is "Specified
> native messaging host not found" with everything looking correct on
> paper. Always run registration in a shell the user opened themselves.

From a native cmd:

```cmd
reg add "HKCU\Software\Google\Chrome\NativeMessagingHosts\com.vaultsign.signer" /ve /t REG_SZ /d "C:\Users\<USER>\Downloads\vaultsignsigner\host\com.vaultsign.signer.json" /f
```

Verify:

```cmd
reg query "HKCU\Software\Google\Chrome\NativeMessagingHosts\com.vaultsign.signer"
```

The `(Default)` value must point to the absolute path of
`com.vaultsign.signer.json`, which in turn has `"path"` pointing to
`vaultsign-host.exe`.

### 3. Load the extension

1. `chrome://extensions` → enable **Developer mode** (top right)
2. **Load unpacked** → select `extension/` folder
3. Confirm extension ID = `ijlgpagoomkbimmghbimheocgneioibn` (deterministic
   from the public key pinned in `manifest.json`)
4. After any source change in `extension/`, hit the **🔄 reload** icon on
   the extension card

### 4. Run the dev server and test page

```powershell
cd host
.\devserver.exe -dir ..\test -addr :8080
```

Open `http://localhost:8080`. Status should turn green
("extensão conectada — native host v0.1.0"). Click **Ping**, **List Certs**,
**Sign Hash**.

### Logs

The host mirrors stderr to `%TEMP%\vaultsign-host.log`. Tail it during
testing — every request the host receives gets logged with the action and
payload size:

```powershell
Get-Content "$env:TEMP\vaultsign-host.log" -Tail 20 -Wait
```

The extension's background service worker logs are at
`chrome://extensions` → on the VaultSign card click the **service worker**
link, look for `[vaultsign-bg] -> host` and `<- host` lines.

## Wire protocol

The page talks to the extension over `window.postMessage` or
`chrome.runtime.sendMessage`; the extension forwards to the host over
length-prefixed JSON Native Messaging (4-byte little-endian length +
UTF-8 JSON body). All three actions are generic enough that any caller
can adopt the protocol:

```
ping:
  →  { "type": "VAULTSIGN_SIGNER_REQUEST", "requestId": "...", "action": "ping" }
  ←  { "type": "VAULTSIGN_SIGNER_RESPONSE", "requestId": "...", "action": "pong", "version": "0.1.1" }

listCertificates:
  →  { ..., "action": "listCertificates" }
  ←  { ..., "action": "listCertificates", "certificates": [
        { "thumbprint", "subject", "issuer", "serialNumber", "notBefore",
          "notAfter", "cn", "cpf"?, "cnpj"?, "hasPrivateKey", "keyUsage": [...] }
      ] }

signHash:
  →  { ..., "action": "signHash", "thumbprint": "...", "hash": "<b64>", "hashAlgorithm": "SHA-256" }
  ←  { ..., "action": "signHash", "signature": "<b64 PKCS#7>", "certificate": "<b64 X.509 DER>" }
```

`thumbprint` is the SHA-1 thumbprint of the cert in hex (uppercase, no
separators) — used as the cert handle in the Windows store.

`hash` is the base64-encoded 32-byte SHA-256 digest the caller wants
signed. The host re-acquires the key container under
`PROV_RSA_AES` (Microsoft Enhanced RSA and AES CSP) so SHA-256 works
even when the cert was imported under a legacy CSP that only supports
SHA-1 (a common situation for ICP-Brasil certs from older PKCS#12
files — see [`NOTES.md`](./NOTES.md) for the gory details).

`signature` is a **detached CMS / PKCS#7 SignedData**. Today it's
CAdES-BES (signature directly over the input digest, no signedAttrs).
RFC 5126 §5.7.3 `signing-certificate-v2` inside signedAttrs is on the
roadmap for full ITI compliance — the wire protocol will gain a
`mode: "bes" | "signedAttrs"` field, default `"bes"` for backwards
compatibility.

## Architecture

```
extension/
  manifest.json          # Manifest V3, key field pins the extension ID
  background.js          # Service worker; single Native Messaging port,
                         # FIFO queue of in-flight requests
  content-script.js      # postMessage relay between page and background
  icons/                 # 16/48/128 PNG (placeholder VS monogram)

host/
  cmd/vaultsign-host/    # Native host binary (stdio Native Messaging)
  cmd/framer/            # Helper to wrap/unwrap length-prefixed JSON
  cmd/extkey/            # Generates extension keypair + computes Chrome ID
  cmd/icongen/           # Generates placeholder icons
  cmd/devserver/         # Serves test/ on http://localhost:8080
  cmd/nmtest/            # Diagnostic: simulates Chrome's host lookup
  internal/protocol/     # Length-prefixed JSON (4-byte LE + bytes)
  internal/certstore/    # CryptoAPI — list + sign on Windows
  internal/cms/          # Detached CMS SignedData built via encoding/asn1
  scripts/               # install-native-host.ps1 (registers HKCU)
  com.vaultsign.signer.template.json
  com.vaultsign.signer.json   # Generated; path/origins filled in

test/
  index.html             # 3-button dev page (ping / list / sign)
  app.js                 # Talks to extension via window.postMessage
```

## Code signing policy

See [CODE_SIGNING_POLICY.md](./CODE_SIGNING_POLICY.md) for the project's
release-signing process, role assignments, and privacy disclosures.

## License

[MIT](./LICENSE) — Copyright (c) 2026 Giuliano Passalacqua /
Rocket99 Ventures LLC.

You can use this code in any project, commercial or otherwise, including
forking the host and pointing it at a different application. The
`allowed_origins` field in the Native Messaging manifest is the
boundary — a fork that wants to serve a different browser extension
just rebuilds with its own list.
