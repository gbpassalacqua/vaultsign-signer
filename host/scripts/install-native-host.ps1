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
# Three signature states matter:
#   1. Valid                - production-signed (Trusted Signing / SignPath
#                             / EV cert). Trust chain reaches a CA in the
#                             Microsoft Trusted Root Program. Leave it
#                             ALONE — re-signing with the dev cert would
#                             downgrade it.
#   2. UnknownError /        - signed but trust chain doesn't terminate
#      NotTrusted              at a trusted root (self-signed dev cert,
#                              or signed by a cert removed from the store).
#                              Re-sign with the current dev cert.
#   3. NotSigned             - no signature at all. Sign with dev cert.
#
# State (1) is the new normal once CI starts shipping signed binaries —
# devs should be able to drop a release artifact into host/ and run this
# script without it stomping the production signature.
$sacOn = (Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\CI\Policy' `
    -Name 'VerifiedAndReputablePolicyState' -ErrorAction SilentlyContinue).VerifiedAndReputablePolicyState
if ($sacOn -eq 1) {
    $existing = Get-AuthenticodeSignature -FilePath $hostExe
    if ($existing.Status -eq "Valid") {
        # Production signature with trusted chain — don't touch.
        Write-Output "Existing signature is Valid (chain trusted) — skipping dev re-sign."
        Write-Output "  Signer: $($existing.SignerCertificate.Subject)"
        Write-Output "  Issuer: $($existing.SignerCertificate.Issuer)"
    } else {
        # NotSigned or UnknownError (e.g. self-signed) — dev path applies.
        $devCert = Get-ChildItem Cert:\CurrentUser\My | Where-Object { $_.Subject -eq 'CN=VaultSign Dev' } | Select-Object -First 1
        if ($null -eq $devCert) {
            Write-Warning "Smart App Control is on but no CN=VaultSign Dev cert in CurrentUser\My - Chrome will fail to launch the host."
            Write-Warning "Generate one and re-run:"
            Write-Warning '  $cert = New-SelfSignedCertificate -Type CodeSigningCert -Subject "CN=VaultSign Dev" -CertStoreLocation Cert:\CurrentUser\My -KeyUsage DigitalSignature -KeyAlgorithm RSA -KeyLength 2048 -NotAfter (Get-Date).AddYears(2)'
        } elseif ($existing.SignerCertificate -and $existing.SignerCertificate.Thumbprint -eq $devCert.Thumbprint) {
            # Already signed with current dev cert; SAC re-evaluates each
            # launch so the signature is still good — no work needed.
            Write-Output "Binary already signed with current dev cert ($($devCert.Thumbprint)) — skipping."
        } else {
            Set-AuthenticodeSignature -FilePath $hostExe -Certificate $devCert | Out-Null
            Write-Output "Re-signed $hostExe with dev cert $($devCert.Thumbprint)"
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
