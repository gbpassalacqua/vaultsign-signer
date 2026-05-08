# Dev Notes

Lessons learned the hard way. Read these before debugging anything weird.

## Chrome Web Store publication — 2026-05-08

Extension was approved and published on the Chrome Web Store on
**May 8, 2026**.

- **Listing URL:** https://chromewebstore.google.com/detail/vaultsign-signer/daaihfchoifcjlkiiafinacokmmphnde
- **Store extension ID:** `daaihfchoifcjlkiiafinacokmmphnde`
- **Dev (Load Unpacked) extension ID:** `ijlgpagoomkbimmghbimheocgneioibn`

The Store ID differs from the dev ID because the Web Store strips the
`key` field from `manifest.json` and re-derives the ID from a fresh
keypair Google manages. We keep the `key` field in `extension/manifest.json`
(dev manifest) so unpacked reloads stay on the stable dev ID, and
strip it from `extension/manifest.store.json` (Store-bound manifest) so
the upload doesn't get rejected.

The native host's `com.vaultsign.signer.json` `allowed_origins` lists
**both IDs** so a single host install works for:

- developers running the unpacked extension locally, AND
- end users who installed the extension from the Web Store.

When changes to that allowlist are needed, edit
`host/com.vaultsign.signer.template.json` (versioned). The
`host/com.vaultsign.signer.json` actually consumed by Chrome is generated
from the template by `host/scripts/install-native-host.ps1` and is
gitignored because the absolute `path` is machine-specific.

## Agent / sandboxed shell quirk: HKCU registry writes can be virtualized

If you're running setup commands from inside an agent runtime (e.g.,
Claude Code, an IDE-integrated assistant, a sandboxed CI runner), HKCU
writes may be redirected to a per-process virtual hive that no other
process — including Chrome — can see.

The symptom is that `reg query` from inside the agent shows the key, but
`reg query` from a native cmd window the user opened shows
`ERROR: The system was unable to find the specified registry key or value.`
This is bad because Chrome reads from the user's real HKCU, so native
messaging lookups silently fail with the misleading "Specified native
messaging host not found" — even though everything looks correct from
inside the agent.

**Rule:** never have the agent write to HKCU itself. Build the `reg add`
command and hand it to the user to paste into a cmd they opened with
Win+R. Verify by having the user run `reg query` from the same cmd; if
the key shows there, Chrome will see it too.

This applies to:
- Native Messaging host registration (`HKCU\Software\Google\Chrome\NativeMessagingHosts\...`)
- Anything else under HKCU that another process needs to consume.

The same caveat *may* apply to filesystem writes outside the project
tree (`%LOCALAPPDATA%`, `%APPDATA%`, `C:\Users\<user>\` root). Writes
inside the project directory have been observed to land in real FS.
When in doubt, have the user verify with `dir` from their cmd.

## Smart App Control (Win11) blocks unsigned binaries that touch crypt32

If SAC is on, `vaultsign-host.exe` gets blocked at exec because it imports
`crypt32.dll`. Self-signing the binary with any cert in
`Cert:\CurrentUser\My` is enough to satisfy SAC's check (it doesn't
verify trust chain, only signature presence). Re-sign after every build —
SAC re-evaluates each launch.

```powershell
$cert = Get-ChildItem Cert:\CurrentUser\My | Where-Object { $_.Subject -eq 'CN=VaultSign Dev' } | Select-Object -First 1
Set-AuthenticodeSignature -FilePath .\host\vaultsign-host.exe -Certificate $cert
```

For production distribution we still need an EV code-signing cert.

## Microsoft Enhanced Cryptographic Provider v1.0 doesn't support SHA-256

Both A1 ICP-Brasil certs we tested were imported under that legacy CSP,
which only supports SHA-1 in `CryptCreateHash`. Calling
`CryptCreateHash(CALG_SHA_256)` on a context bound to that CSP returns
`NTE_BAD_ALGID`.

The workaround in `host/internal/certstore/windows.go` is to read the
cert's `CRYPT_KEY_PROV_INFO`, then re-acquire the key container under
`PROV_RSA_AES` (Microsoft Enhanced RSA and AES Cryptographic Provider,
which supports SHA-256 natively). Same key, different provider face.

## CryptoAPI legacy returns signature in little-endian

`CryptSignHash` emits the RSA signature in little-endian word order (a
CryptoAPI legacy artifact). PKCS#1 v1.5 / CMS expects big-endian.
Reverse the bytes before wrapping in PKCS#7.

## Chrome 147 ignores `--load-extension` on stable

Launching Chrome with `--load-extension=path` shows a warning in
`chrome_debug.log` ("--load-extension is not allowed in Google Chrome,
ignoring") and skips the load. This is a deliberate Google restriction
on stable Chrome.

For dev: install via `chrome://extensions` → Load unpacked. The pinned
`key` field in `manifest.json` keeps the extension ID stable across
manual reloads.

## Native Messaging host registry redirection — Chrome reads HKCU AND HKLM

Chrome looks up the host name in:
1. `HKCU\Software\Google\Chrome\NativeMessagingHosts\<name>`
2. `HKLM\Software\WOW6432Node\Google\Chrome\NativeMessagingHosts\<name>`
3. `HKLM\Software\Google\Chrome\NativeMessagingHosts\<name>`

For dev, HKCU is sufficient and doesn't need admin. For installer
distribution we'll need HKLM (machine-wide) plus admin privileges.
