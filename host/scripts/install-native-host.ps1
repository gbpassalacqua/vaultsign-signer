# install-native-host.ps1 - registers vaultsign-host as a Chrome Native
# Messaging host for the current user (HKCU; no admin needed).
#
# Resolves the host binary and manifest paths relative to this script,
# substitutes them into the template, writes the manifest, then points
# the registry key at it. Idempotent.
#
# Usage:
#   .\install-native-host.ps1                     # uses default extension ID
#   .\install-native-host.ps1 -ExtensionId <id>   # override

param(
    [string]$ExtensionId = "ijlgpagoomkbimmghbimheocgneioibn",
    [switch]$Uninstall
)

$ErrorActionPreference = "Stop"

$hostName     = "com.vaultsign.signer"
$hostDir      = (Resolve-Path "$PSScriptRoot\..").Path
$hostExe      = Join-Path $hostDir "vaultsign-host.exe"
$templatePath = Join-Path $hostDir "com.vaultsign.signer.template.json"
$manifestPath = Join-Path $hostDir "com.vaultsign.signer.json"
$regPath      = "HKCU:\Software\Google\Chrome\NativeMessagingHosts\$hostName"

if ($Uninstall) {
    if (Test-Path $regPath) {
        Remove-Item -Path $regPath -Force
        Write-Output "Removed registry key: $regPath"
    } else {
        Write-Output "Registry key not present: $regPath"
    }
    if (Test-Path $manifestPath) {
        Remove-Item -Path $manifestPath -Force
        Write-Output "Removed manifest: $manifestPath"
    }
    return
}

if (-not (Test-Path $hostExe)) {
    Write-Error "Native host binary not found at $hostExe - build it first (cd host; go build -o vaultsign-host.exe ./cmd/vaultsign-host)"
    exit 1
}
if (-not (Test-Path $templatePath)) {
    Write-Error "Template manifest not found at $templatePath"
    exit 1
}

# Authenticode-sign the binary if Smart App Control is on AND the
# binary doesn't already carry a valid (trusted-chain) signature.
#
# Signature states + handling (triple-safe):
#
#   Status = Valid
#       → Production-signed (Microsoft Trusted Signing, EV cert, etc).
#         Chain reaches a Microsoft-Trusted Root. LEAVE IT ALONE —
#         re-signing would downgrade the trust level. This is the
#         expected state for CI-built release artifacts.
#
#   Status != Valid AND SignerCertificate.Thumbprint matches current
#   dev cert
#       → Already self-signed with this machine's dev cert. Idempotent
#         skip; SAC re-evaluates on each launch but the existing sig
#         is fine.
#
#   Status = NotSigned
#       → No signature at all. Sign with dev cert.
#
#   Status != Valid AND SignerCertificate.Subject = "CN=VaultSign Dev"
#   (stale dev cert, e.g. expired or different machine)
#       → Re-sign with current dev cert.
#
#   Status != Valid AND signed by an UNKNOWN certificate (not Valid,
#   not VaultSign Dev)
#       → Refuse to re-sign. Warn loudly and exit. This protects
#         against an edge case where a partially-trusted production
#         cert (e.g. cross-sign issue, expired Microsoft cert, or a
#         cert whose chain is broken for environmental reasons) would
#         otherwise be silently replaced with a self-signed dev cert.
#         Operator can manually inspect with Get-AuthenticodeSignature
#         and decide what to do.
$sacOn = (Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\CI\Policy' `
    -Name 'VerifiedAndReputablePolicyState' -ErrorAction SilentlyContinue).VerifiedAndReputablePolicyState
if ($sacOn -eq 1) {
    $existing = Get-AuthenticodeSignature -FilePath $hostExe
    if ($existing.Status -eq "Valid") {
        Write-Output "Existing signature is Valid (chain trusted) - skipping dev re-sign."
        Write-Output ("  Signer: {0}" -f $existing.SignerCertificate.Subject)
        Write-Output ("  Issuer: {0}" -f $existing.SignerCertificate.Issuer)
        Write-Output ("  Thumb:  {0}" -f $existing.SignerCertificate.Thumbprint)
    } else {
        $devCert = Get-ChildItem Cert:\CurrentUser\My | Where-Object { $_.Subject -eq 'CN=VaultSign Dev' } | Select-Object -First 1
        $existingSubject = if ($existing.SignerCertificate) { $existing.SignerCertificate.Subject } else { $null }
        $existingThumb = if ($existing.SignerCertificate) { $existing.SignerCertificate.Thumbprint } else { $null }

        if ($null -eq $devCert) {
            Write-Warning "Smart App Control is on but no CN=VaultSign Dev cert in CurrentUser\My - Chrome will fail to launch the host."
            Write-Warning "Generate one and re-run:"
            Write-Warning '  $cert = New-SelfSignedCertificate -Type CodeSigningCert -Subject "CN=VaultSign Dev" -CertStoreLocation Cert:\CurrentUser\My -KeyUsage DigitalSignature -KeyAlgorithm RSA -KeyLength 2048 -NotAfter (Get-Date).AddYears(2)'
        } elseif ($existingThumb -eq $devCert.Thumbprint) {
            Write-Output ("Binary already signed with current dev cert ({0}) - skipping." -f $devCert.Thumbprint)
        } elseif ($existing.Status -eq "NotSigned" -or $existingSubject -eq "CN=VaultSign Dev") {
            # Either unsigned, or signed with a stale "CN=VaultSign Dev"
            # cert (different thumbprint, e.g. older dev cert that was
            # regenerated). Safe to overwrite.
            Set-AuthenticodeSignature -FilePath $hostExe -Certificate $devCert | Out-Null
            Write-Output ("Signed {0} with dev cert {1}" -f $hostExe, $devCert.Thumbprint)
        } else {
            Write-Warning ("Binary is signed (Status={0}) by an UNKNOWN certificate:" -f $existing.Status)
            Write-Warning ("  Subject:    {0}" -f $existingSubject)
            Write-Warning ("  Thumbprint: {0}" -f $existingThumb)
            Write-Warning "Refusing to replace this signature with the dev cert."
            Write-Warning "If you want to force a dev re-sign, run manually:"
            Write-Warning ('  Set-AuthenticodeSignature -FilePath "{0}" -Certificate (Get-ChildItem Cert:\CurrentUser\My\{1})' -f $hostExe, $devCert.Thumbprint)
            Write-Warning "Otherwise the existing signature stays and Chrome may fail to launch the host under SAC."
        }
    }
}

# Build the manifest from template.
$manifest = (Get-Content $templatePath -Raw).
    Replace("__HOST_PATH__", $hostExe.Replace("\", "\\")).
    Replace("__EXTENSION_ID__", $ExtensionId)

# Write WITHOUT BOM - Chrome's manifest parser is strict.
$utf8NoBom = New-Object System.Text.UTF8Encoding $false
[System.IO.File]::WriteAllText($manifestPath, $manifest, $utf8NoBom)

# Register in HKCU. The "(Default)" property is the unnamed default value.
if (-not (Test-Path $regPath)) {
    New-Item -Path $regPath -Force | Out-Null
}
Set-ItemProperty -Path $regPath -Name "(Default)" -Value $manifestPath

Write-Output ""
Write-Output "OK - VaultSign Signer native host registered:"
Write-Output ("  host binary  : {0}" -f $hostExe)
Write-Output ("  manifest     : {0}" -f $manifestPath)
Write-Output ("  registry     : {0}" -f $regPath)
Write-Output ("  extension ID : {0}" -f $ExtensionId)
Write-Output ""
Write-Output "Next: load the unpacked extension from .\extension\ in chrome://extensions"
Write-Output "      (Developer mode -> Load unpacked) and confirm the ID matches above."
