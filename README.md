# VaultSign Signer

Native host + (futuro) extensão Chrome que permite o [VaultSign](https://vaultsign.app)
acessar certificados digitais ICP-Brasil instalados na máquina do signatário e
assinar documentos PDF com assinatura qualificada.

A chave privada **nunca sai da máquina do signatário** — só trafega o hash
SHA-256 do PDF (32 bytes) e o PKCS#7 resultante (~3 KB).

## Status

**Em desenvolvimento — núcleo de 2 dias.** Hoje só o native host Go para Windows
com legacy CryptoAPI (A1). Sem extensão, sem instalador, sem macOS/Linux.

## Pré-requisitos

- Windows 10/11 com certificado ICP-Brasil A1 importado (.pfx) no store `My`
  do `CurrentUser`.
- Go 1.23+.

## Build

```powershell
cd host
go build -o vaultsign-host.exe ./cmd/vaultsign-host
go build -o framer.exe ./cmd/framer
```

### Smart App Control (Win11)

If Smart App Control is on, unsigned binaries that touch `crypt32.dll` get
blocked at exec. Two workarounds, in increasing order of legitimacy:

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

   # After every rebuild
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

## Teste standalone

```powershell
# Listar certificados
.\framer.exe encode '{"action":"listCertificates"}' | .\vaultsign-host.exe | .\framer.exe decode

# Ping
.\framer.exe encode '{"action":"ping"}' | .\vaultsign-host.exe | .\framer.exe decode
```

## Arquitetura

```
host/
  cmd/vaultsign-host/   # Native host binary (stdio Native Messaging)
  cmd/framer/           # Helper para envelope length-prefixed JSON em testes
  internal/protocol/    # Length-prefixed JSON (4-byte LE prefix + bytes)
  internal/certstore/   # Windows CryptoAPI — list + (em breve) sign
```
