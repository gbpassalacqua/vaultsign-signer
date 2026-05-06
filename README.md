# VaultSign Signer

Chrome extension + native host that lets [VaultSign](https://vaultsign.app)
access ICP-Brasil digital certificates installed on the signer's machine and
sign PDF documents with a qualified signature.

The private key **never leaves the signer's machine** — only the SHA-256
hash of the PDF (32 bytes) crosses the wire to the host, and the PKCS#7
(~3 KB) crosses back.

## Status

**Day 3 done.** Working end-to-end on Windows: page → content script →
background service worker → native host → Windows CryptoAPI → PKCS#7 back.
List certs and sign hash both validated. No installer yet, no macOS/Linux,
no `@signpdf` integration, no Web Store publish.

## Prerequisites

- Windows 10/11 with an ICP-Brasil A1 certificate (.pfx) imported into
  `CurrentUser\My` cert store.
- Go 1.23+.
- Google Chrome.

## Build

```powershell
cd host
go build -o vaultsign-host.exe ./cmd/vaultsign-host
go build -o framer.exe        ./cmd/framer
go build -o extkey.exe        ./cmd/extkey       # one-time: extension key + ID
go build -o icongen.exe       ./cmd/icongen      # one-time: placeholder icons
go build -o devserver.exe     ./cmd/devserver    # serves test/ on :8080
```

### Smart App Control (Win11)

Smart App Control on Windows 11 blocks unsigned binaries that touch
`crypt32.dll` at exec. Two workarounds, in increasing order of legitimacy:

1. **Self-sign the dev build (preferred during development).** Signing
   the binary — even with a self-signed cert that's not in any trust
   store — was enough to satisfy SAC in our testing:

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

2. **Disable Smart App Control entirely** (one-way — can't be re-enabled
   without reinstalling Windows): Settings → Windows Security → App &
   browser control → Smart App Control settings → Off → confirm in the
   warning dialog → reboot. Verify with
   `reg query "HKLM\SYSTEM\CurrentControlSet\Control\CI\Policy" /v VerifiedAndReputablePolicyState`
   showing `0x0`.

For production distribution we need a real EV code-signing cert anyway —
that's separate from the dev workaround above.

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
entry pointing Chrome at the manifest. After every rebuild:

### 1. Re-sign the host binary

Required if Smart App Control is on. Without a fresh signature, Chrome
silently fails to launch the binary after a rebuild:

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

## License

[MIT](./LICENSE) — Copyright (c) 2026 Giuliano Passalacqua / Rocket99 Ventures LLC.
