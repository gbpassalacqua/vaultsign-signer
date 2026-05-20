# VaultSign Signer — Windows Installer

This directory contains the [Inno Setup](https://jrsoftware.org/isinfo.php)
script that produces `VaultSignSigner-Setup-<version>.exe` — a per-user
Windows installer that delivers `vaultsign-host.exe` to
`%LOCALAPPDATA%\VaultSign\Signer\` and registers it as a Chrome Native
Messaging host for the published extension
(`daaihfchoifcjlkiiafinacokmmphnde`).

The installer requires **no admin privileges**. It runs entirely under the
user's profile, with no UAC prompt.

## What gets installed

```
%LOCALAPPDATA%\VaultSign\Signer\
  vaultsign-host.exe              (Microsoft Trusted Signing-signed)
  com.vaultsign.signer.json       (generated at install time, absolute path)

HKCU\Software\Google\Chrome\NativeMessagingHosts\com.vaultsign.signer
  (Default)  = %LOCALAPPDATA%\VaultSign\Signer\com.vaultsign.signer.json

Start Menu\Programs\VaultSign Signer\
  VaultSign Signer.url             → https://vaultsign.app
  Desinstalar VaultSign Signer.lnk → uninst000.exe
```

Uninstaller removes all of the above plus the empty `VaultSign\` parent
directory.

## Build locally

Install Inno Setup (one-time):

```powershell
choco install innosetup --no-progress -y
```

From the repo root:

```powershell
# Stage the helper next to the .iss so Inno's [Files] section finds it
Copy-Item host\vaultsign-host.exe installer\vaultsign-host.exe

# Compile — AppVersion goes into the filename and the UninstallDisplayName
iscc /DAppVersion=0.1.3-local installer\vaultsign-signer.iss
```

Output:

```
installer\Output\VaultSignSigner-Setup-0.1.3-local.exe
```

The local build is **unsigned**. Smart App Control will block its launch
on Windows 11. For local end-to-end testing on a SAC-enabled machine,
sign it with your dev cert first:

```powershell
$cert = Get-ChildItem Cert:\CurrentUser\My | Where-Object Subject -eq 'CN=VaultSign Dev'
Set-AuthenticodeSignature installer\Output\VaultSignSigner-Setup-0.1.3-local.exe -Certificate $cert
```

SAC accepts any Authenticode signature for the SAC-block bypass, including
self-signed ones not in any trust store. (See `NOTES.md` in the repo
root for the history of that workaround.)

## CI build

The release workflow `.github/workflows/build-sign-release.yml` runs on
every `v*` tag push and produces signed assets:

1. `go build` produces `host/vaultsign-host.exe`
2. `azure/trusted-signing-action@v0.5.1` signs the helper
3. Helper is copied into `installer/vaultsign-host.exe`
4. Inno Setup is installed via Chocolatey
5. `iscc` compiles `installer/Output/VaultSignSigner-Setup-<version>.exe`
6. `azure/trusted-signing-action@v0.5.1` runs again, this time against
   `installer/Output/`, signing the Setup.exe outer wrapper
7. Both `vaultsign-host.exe` and `VaultSignSigner-Setup-<version>.exe`
   are uploaded as Release assets

The `<version>` part of the filename is the tag with the leading `v`
stripped: tag `v0.1.3` → `VaultSignSigner-Setup-0.1.3.exe`.

The whole flow runs in a single workflow job so both signatures share the
same Azure session and the same Trusted Signing certificate validity
window. The helper inside the installer keeps its own signature
(Authenticode is a wrapping format, not a transform — the bytes pass
through unmodified) so after extraction it is independently verifiable.

## Federated Identity Credential (FIC) per release

Azure Trusted Signing authenticates the workflow against GitHub OIDC via
a Federated Identity Credential on the App Registration. Until we move
to a wildcard-subject FIC, **each new release tag needs a matching FIC
created before the tag is pushed** — otherwise `azure/login@v2` fails
with `AADSTS70021: No matching federated identity record found`.

Run this in [Azure Cloud Shell](https://shell.azure.com) **before** pushing
the tag:

```bash
TAG="v0.1.3"                                  # the tag you are about to push
APP_OBJECT_ID="<paste-from-Entra-portal>"     # one-time lookup; same value each time

az ad app federated-credential create \
  --id "$APP_OBJECT_ID" \
  --parameters @- <<EOF
{
  "name": "github-actions-${TAG}",
  "issuer": "https://token.actions.githubusercontent.com",
  "subject": "repo:gbpassalacqua/vaultsign-signer:ref:refs/tags/${TAG}",
  "description": "GitHub Actions release build for ${TAG}",
  "audiences": ["api://AzureADTokenExchange"]
}
EOF
```

This is a one-second operation. After it succeeds you can push the tag:

```bash
git tag v0.1.3 && git push --tags
```

After 3–4 successful releases without incident, consider switching to a
wildcard-subject FIC (`refs/tags/v*` via Entra's "flexible federated
identity credentials") to drop the per-release step. That feature was
still in preview as of late 2025, so stability-first means exact-match
FIC for now.

## Troubleshooting

### SmartScreen "Windows protected your PC" warning

Even with Microsoft Trusted Signing, end users may see a SmartScreen
warning on the very first downloads of a freshly-released installer.
SmartScreen tracks each unique binary hash and clears the warning once
the file accumulates clean download history.

Mitigation:

- Tell early users to click **"More info" → "Run anyway"** to proceed
- Watch GitHub release download counts; warnings typically clear after
  ~hundreds of installs without abuse reports
- Reputation does **not** transfer between releases — every new version
  starts from zero. Frequent tiny releases hurt reputation buildup;
  prefer larger well-tested ones
- An EV (Extended Validation) code-signing cert pre-builds reputation,
  but Trusted Signing is the closest legitimate alternative without the
  ~$300/year EV cost

If a customer reports the warning persisting more than ~24h after
release, verify the file wasn't tampered in transit (corporate proxies
sometimes strip signatures):

```powershell
Get-AuthenticodeSignature C:\path\to\VaultSignSigner-Setup-X.Y.Z.exe | Format-List
```

A healthy result has:

- `Status: Valid`
- `SignerCertificate.Subject: CN=Giuliano Bandini Passalacqua, …`
- A non-null `TimeStamperCertificate`

Anything else means the file was modified after we signed it.

### `iscc: command not found` on local build

`choco install innosetup` should add `C:\Program Files (x86)\Inno Setup 6\`
to PATH. If PATH wasn't refreshed in the current shell, open a new
PowerShell window or invoke the absolute path:

```powershell
& "C:\Program Files (x86)\Inno Setup 6\ISCC.exe" /DAppVersion=0.1.3-local installer\vaultsign-signer.iss
```

### `[Files] entry not found: vaultsign-host.exe`

You forgot to stage the helper. From repo root:

```powershell
Copy-Item host\vaultsign-host.exe installer\vaultsign-host.exe
```

## Files

| File                    | Purpose                                  |
|-------------------------|------------------------------------------|
| `vaultsign-signer.iss`  | Inno Setup script (versioned)            |
| `README.md`             | this file                                |
| `Output/`               | local build output (gitignored via `*.exe`) |
| `vaultsign-host.exe`    | local-only staging copy (gitignored via `*.exe`) |
