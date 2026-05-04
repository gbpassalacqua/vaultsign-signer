//go:build windows

// Package certstore enumerates ICP-Brasil certificates from the Windows
// CurrentUser\My certificate store. Day-1 scope: list only. Sign comes Day 2.
package certstore

import (
	"bytes"
	"crypto/sha1"
	"crypto/x509"
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// CRYPT_E_NOT_FOUND — returned by CertEnumCertificatesInStore when the
// store has been exhausted. Treated as the normal stop condition.
const cryptENotFound = 0x80092004

// CERT_KEY_PROV_INFO_PROP_ID is the property ID for CRYPT_KEY_PROV_INFO,
// which is set on cert contexts that are linked to a private key in some
// CSP/KSP. Presence is a cheap proxy for "has private key" without
// triggering any key access (and therefore no PIN prompt).
const certKeyProvInfoPropID = 2

// ListCertificates returns the ICP-Brasil-eligible certificates from
// the user's "My" store. Self-signed and non-AC-issued certs are filtered
// out — those are typically test/dev artifacts left over by other tools.
func ListCertificates() ([]CertInfo, error) {
	storeName, err := windows.UTF16PtrFromString("MY")
	if err != nil {
		return nil, err
	}
	store, err := windows.CertOpenStore(
		windows.CERT_STORE_PROV_SYSTEM,
		0,
		0,
		windows.CERT_SYSTEM_STORE_CURRENT_USER,
		uintptr(unsafe.Pointer(storeName)),
	)
	if err != nil {
		return nil, fmt.Errorf("CertOpenStore: %w", err)
	}
	defer windows.CertCloseStore(store, 0)

	var out []CertInfo
	var ctx *windows.CertContext
	for {
		ctx, err = windows.CertEnumCertificatesInStore(store, ctx)
		if ctx == nil {
			if err != nil && !isErrno(err, cryptENotFound) {
				return nil, fmt.Errorf("CertEnumCertificatesInStore: %w", err)
			}
			break
		}
		info, ok := certInfoFromContext(ctx)
		if ok {
			out = append(out, info)
		}
	}
	return out, nil
}

func isErrno(err error, code uintptr) bool {
	var en windows.Errno
	return errors.As(err, &en) && uintptr(en) == code
}

func certInfoFromContext(ctx *windows.CertContext) (CertInfo, bool) {
	// Copy the DER bytes out — the context is freed when the loop calls
	// CertEnumCertificatesInStore again with prevContext = ctx.
	derSrc := unsafe.Slice(ctx.EncodedCert, int(ctx.Length))
	der := make([]byte, len(derSrc))
	copy(der, derSrc)

	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return CertInfo{}, false
	}

	// Filter 1: skip self-signed (subject == issuer DER bytes).
	// `certutil` showed at least one stray self-signed GUID-named cert in
	// the user's store from another tool — drop it.
	if bytes.Equal(cert.RawSubject, cert.RawIssuer) {
		return CertInfo{}, false
	}

	// Filter 2: ICP-Brasil ACs always have CN starting with "AC ".
	// (e.g., "AC SyngularID Multipla", "AC Certisign", "AC Soluti".)
	if !strings.HasPrefix(cert.Issuer.CommonName, "AC ") {
		return CertInfo{}, false
	}

	// Filter 3: must support digitalSignature or nonRepudiation (contentCommitment).
	if cert.KeyUsage&(x509.KeyUsageDigitalSignature|x509.KeyUsageContentCommitment) == 0 {
		return CertInfo{}, false
	}

	cpf, cnpj := parseICPBrasilSAN(cert)
	if cpf == "" && cnpj == "" {
		cpf, cnpj = parseFromCN(cert.Subject.CommonName)
	}

	sum := sha1.Sum(der)
	return CertInfo{
		Thumbprint:    strings.ToUpper(hex.EncodeToString(sum[:])),
		Subject:       cert.Subject.String(),
		Issuer:        cert.Issuer.String(),
		SerialNumber:  fmt.Sprintf("%x", cert.SerialNumber),
		NotBefore:     cert.NotBefore.UTC().Format(time.RFC3339),
		NotAfter:      cert.NotAfter.UTC().Format(time.RFC3339),
		CN:            cert.Subject.CommonName,
		CPF:           cpf,
		CNPJ:          cnpj,
		HasPrivateKey: certHasPrivateKey(ctx),
		KeyUsage:      keyUsageStrings(cert.KeyUsage),
	}, true
}

var (
	modCrypt32                            = windows.NewLazySystemDLL("crypt32.dll")
	procCertGetCertificateContextProperty = modCrypt32.NewProc("CertGetCertificateContextProperty")

	modAdvapi32              = windows.NewLazySystemDLL("advapi32.dll")
	procCryptAcquireContextW = modAdvapi32.NewProc("CryptAcquireContextW")
	procCryptReleaseContext  = modAdvapi32.NewProc("CryptReleaseContext")
	procCryptCreateHash      = modAdvapi32.NewProc("CryptCreateHash")
	procCryptSetHashParam    = modAdvapi32.NewProc("CryptSetHashParam")
	procCryptSignHashW       = modAdvapi32.NewProc("CryptSignHashW")
	procCryptDestroyHash     = modAdvapi32.NewProc("CryptDestroyHash")
)

// CryptoAPI legacy constants. We always sign through PROV_RSA_AES rather
// than the cert's bound provider because A1 ICP-Brasil certs are typically
// imported under "Microsoft Enhanced Cryptographic Provider v1.0" which
// pre-dates SHA-256 — CryptCreateHash with CALG_SHA_256 returns NTE_BAD_ALGID
// on that provider. PROV_RSA_AES sees the same key container and supports
// SHA-256 natively.
const (
	provRsaAes  = 24         // PROV_RSA_AES
	calgSha256  = 0x0000800c // CALG_SHA_256
	hpHashval   = 2          // HP_HASHVAL
	atSignature = 2          // AT_SIGNATURE — the canonical key spec for sign-only RSA keys
)

// certHasPrivateKey checks for the CERT_KEY_PROV_INFO property without
// actually acquiring the key — so no PIN prompt fires for A3 tokens.
func certHasPrivateKey(ctx *windows.CertContext) bool {
	var size uint32
	r1, _, _ := procCertGetCertificateContextProperty.Call(
		uintptr(unsafe.Pointer(ctx)),
		uintptr(certKeyProvInfoPropID),
		0, // pvData = nil → just probe size
		uintptr(unsafe.Pointer(&size)),
	)
	return r1 != 0 && size > 0
}

// cryptKeyProvInfoRaw mirrors the C struct CRYPT_KEY_PROV_INFO. The
// pointers refer back into the buffer returned by
// CertGetCertificateContextProperty, so use the data immediately and
// don't store the struct beyond the buffer's lifetime — we copy the
// container name out before returning.
type cryptKeyProvInfoRaw struct {
	ContainerName  *uint16
	ProvName       *uint16
	ProvType       uint32
	Flags          uint32
	ProvParamCount uint32
	ProvParam      uintptr
	KeySpec        uint32
}

// keyProvInfo is the Go-managed snapshot we hand around internally.
type keyProvInfo struct {
	ContainerName []uint16 // null-terminated UTF-16
	KeySpec       uint32
}

// SignHash signs an externally-computed SHA-256 digest with the private
// key bound to the certificate identified by thumbprint. Returns the raw
// signature bytes (big-endian, ready for CMS/PKCS#1) plus the cert DER.
//
// The caller never sees the private key. The hash flows in, the
// signature flows out — that is the entire trust boundary.
func SignHash(thumbprint string, hash []byte) (rawSig []byte, certDER []byte, err error) {
	if len(hash) != 32 {
		return nil, nil, fmt.Errorf("expected 32-byte SHA-256 digest, got %d", len(hash))
	}

	ctx, err := findByThumbprint(thumbprint)
	if err != nil {
		return nil, nil, err
	}
	defer windows.CertFreeCertificateContext(ctx)

	derSrc := unsafe.Slice(ctx.EncodedCert, int(ctx.Length))
	certDER = make([]byte, len(derSrc))
	copy(certDER, derSrc)

	info, err := getKeyProvInfo(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("get CRYPT_KEY_PROV_INFO: %w", err)
	}

	// Acquire context against the key's container using PROV_RSA_AES.
	// Provider name = NULL → uses the default for PROV_RSA_AES, i.e.
	// "Microsoft Enhanced RSA and AES Cryptographic Provider".
	var hProv uintptr
	var containerPtr uintptr
	if len(info.ContainerName) > 0 {
		containerPtr = uintptr(unsafe.Pointer(&info.ContainerName[0]))
	}
	r1, _, callErr := procCryptAcquireContextW.Call(
		uintptr(unsafe.Pointer(&hProv)),
		containerPtr,
		0,                  // pszProvider = NULL → default for ProvType
		uintptr(provRsaAes), // dwProvType = PROV_RSA_AES (supports SHA-256)
		0,                  // dwFlags = 0 → full access for signing
	)
	if r1 == 0 {
		return nil, nil, fmt.Errorf("CryptAcquireContextW (PROV_RSA_AES, container=%q): %w",
			utf16ToString(info.ContainerName), callErr)
	}
	defer procCryptReleaseContext.Call(hProv, 0)

	// Create a SHA-256 hash object and inject the externally-computed digest.
	var hHash uintptr
	r1, _, callErr = procCryptCreateHash.Call(
		hProv, uintptr(calgSha256), 0, 0, uintptr(unsafe.Pointer(&hHash)),
	)
	if r1 == 0 {
		return nil, nil, fmt.Errorf("CryptCreateHash(CALG_SHA_256): %w", callErr)
	}
	defer procCryptDestroyHash.Call(hHash)

	r1, _, callErr = procCryptSetHashParam.Call(
		hHash, uintptr(hpHashval), uintptr(unsafe.Pointer(&hash[0])), 0,
	)
	if r1 == 0 {
		return nil, nil, fmt.Errorf("CryptSetHashParam(HP_HASHVAL): %w", callErr)
	}

	// Sign — first call to size, second to fill. Use the KeySpec from the
	// cert's CRYPT_KEY_PROV_INFO (typically AT_SIGNATURE for ICP-Brasil).
	keySpec := info.KeySpec
	if keySpec == 0 {
		keySpec = atSignature
	}
	var sigLen uint32
	r1, _, callErr = procCryptSignHashW.Call(
		hHash, uintptr(keySpec), 0, 0, 0, uintptr(unsafe.Pointer(&sigLen)),
	)
	if r1 == 0 {
		return nil, nil, fmt.Errorf("CryptSignHash size query: %w", callErr)
	}

	rawSig = make([]byte, sigLen)
	r1, _, callErr = procCryptSignHashW.Call(
		hHash, uintptr(keySpec), 0, 0,
		uintptr(unsafe.Pointer(&rawSig[0])),
		uintptr(unsafe.Pointer(&sigLen)),
	)
	if r1 == 0 {
		return nil, nil, fmt.Errorf("CryptSignHash: %w", callErr)
	}
	rawSig = rawSig[:sigLen]

	// CryptoAPI legacy emits the RSA signature in little-endian word order.
	// CMS / PKCS#1 expect big-endian. Reverse in place.
	for i, j := 0, len(rawSig)-1; i < j; i, j = i+1, j-1 {
		rawSig[i], rawSig[j] = rawSig[j], rawSig[i]
	}

	return rawSig, certDER, nil
}

func findByThumbprint(thumbprint string) (*windows.CertContext, error) {
	storeName, err := windows.UTF16PtrFromString("MY")
	if err != nil {
		return nil, err
	}
	store, err := windows.CertOpenStore(
		windows.CERT_STORE_PROV_SYSTEM,
		0, 0,
		windows.CERT_SYSTEM_STORE_CURRENT_USER,
		uintptr(unsafe.Pointer(storeName)),
	)
	if err != nil {
		return nil, fmt.Errorf("CertOpenStore: %w", err)
	}

	target := strings.ToUpper(thumbprint)
	var ctx, dup *windows.CertContext
	for {
		ctx, err = windows.CertEnumCertificatesInStore(store, ctx)
		if ctx == nil {
			break
		}
		der := unsafe.Slice(ctx.EncodedCert, int(ctx.Length))
		sum := sha1.Sum(der)
		if strings.ToUpper(hex.EncodeToString(sum[:])) == target {
			dup = windows.CertDuplicateCertificateContext(ctx)
			break
		}
	}
	windows.CertCloseStore(store, 0)
	if dup == nil {
		return nil, fmt.Errorf("certificate %q not found in CurrentUser\\My", thumbprint)
	}
	return dup, nil
}

func getKeyProvInfo(ctx *windows.CertContext) (*keyProvInfo, error) {
	var size uint32
	r1, _, _ := procCertGetCertificateContextProperty.Call(
		uintptr(unsafe.Pointer(ctx)),
		uintptr(certKeyProvInfoPropID),
		0,
		uintptr(unsafe.Pointer(&size)),
	)
	if r1 == 0 || size == 0 {
		return nil, errors.New("cert has no CERT_KEY_PROV_INFO property")
	}

	buf := make([]byte, size)
	r1, _, _ = procCertGetCertificateContextProperty.Call(
		uintptr(unsafe.Pointer(ctx)),
		uintptr(certKeyProvInfoPropID),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&size)),
	)
	if r1 == 0 {
		return nil, errors.New("CertGetCertificateContextProperty failed")
	}

	raw := (*cryptKeyProvInfoRaw)(unsafe.Pointer(&buf[0]))
	info := &keyProvInfo{
		KeySpec: raw.KeySpec,
	}
	if raw.ContainerName != nil {
		// Walk the null-terminated UTF-16 string and copy into Go memory
		// so the caller can keep using it after `buf` is GC'd.
		n := 0
		for *(*uint16)(unsafe.Add(unsafe.Pointer(raw.ContainerName), uintptr(n*2))) != 0 {
			n++
		}
		info.ContainerName = make([]uint16, n+1) // include NUL
		copy(info.ContainerName, unsafe.Slice(raw.ContainerName, n+1))
	}
	return info, nil
}

func utf16ToString(s []uint16) string {
	if len(s) == 0 {
		return ""
	}
	// Drop trailing NUL if present.
	if s[len(s)-1] == 0 {
		s = s[:len(s)-1]
	}
	return windows.UTF16ToString(s)
}

// parseICPBrasilSAN extracts CPF (PF) or CNPJ (PJ) from the certificate's
// SubjectAltName otherName entries per ICP-Brasil DOC-ICP-04:
//
//   2.16.76.1.3.1 — PF: 8-char birthdate + 11-char CPF + 11-char NIS + 15-char RG + 6-char issuer
//   2.16.76.1.3.3 — PJ: 14-char CNPJ
//
// Go's crypto/x509 discards otherName entries from SAN, so we re-parse the
// raw extension manually.
func parseICPBrasilSAN(cert *x509.Certificate) (cpf, cnpj string) {
	sanOID := asn1.ObjectIdentifier{2, 5, 29, 17}
	icpPFOID := asn1.ObjectIdentifier{2, 16, 76, 1, 3, 1}
	icpPJOID := asn1.ObjectIdentifier{2, 16, 76, 1, 3, 3}

	for _, ext := range cert.Extensions {
		if !ext.Id.Equal(sanOID) {
			continue
		}
		var sans []asn1.RawValue
		if _, err := asn1.Unmarshal(ext.Value, &sans); err != nil {
			continue
		}
		for _, gn := range sans {
			// otherName is GeneralName CHOICE [0] IMPLICIT — context-specific tag 0, constructed.
			if gn.Class != asn1.ClassContextSpecific || gn.Tag != 0 || !gn.IsCompound {
				continue
			}
			// Re-tag as universal SEQUENCE so encoding/asn1 can unmarshal as OtherName.
			raw := append([]byte(nil), gn.FullBytes...)
			raw[0] = 0x30
			var on struct {
				OID   asn1.ObjectIdentifier
				Value asn1.RawValue `asn1:"explicit,tag:0"`
			}
			if _, err := asn1.Unmarshal(raw, &on); err != nil {
				continue
			}
			s := extractStringish(on.Value.FullBytes)
			switch {
			case on.OID.Equal(icpPFOID) && len(s) >= 19:
				cpf = onlyDigits(s[8:19])
			case on.OID.Equal(icpPJOID) && len(s) >= 14:
				cnpj = onlyDigits(s[:14])
			}
		}
	}
	return
}

// extractStringish unwraps a single ASN.1 string-like value (OCTET STRING,
// PrintableString, UTF8String, IA5String) and returns its content.
func extractStringish(der []byte) string {
	if len(der) < 2 {
		return ""
	}
	// Try OCTET STRING first (most common for ICP-Brasil).
	var oct []byte
	if _, err := asn1.Unmarshal(der, &oct); err == nil {
		return string(oct)
	}
	var s string
	if _, err := asn1.Unmarshal(der, &s); err == nil {
		return s
	}
	return ""
}

// parseFromCN is a fallback for certs where SAN parsing fails. ICP-Brasil
// CNs follow the convention "<name>:<CPF or CNPJ digits>".
func parseFromCN(cn string) (cpf, cnpj string) {
	idx := strings.LastIndex(cn, ":")
	if idx < 0 || idx == len(cn)-1 {
		return "", ""
	}
	digits := onlyDigits(cn[idx+1:])
	switch len(digits) {
	case 11:
		return digits, ""
	case 14:
		return "", digits
	}
	return "", ""
}

func onlyDigits(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func keyUsageStrings(ku x509.KeyUsage) []string {
	mapping := []struct {
		bit  x509.KeyUsage
		name string
	}{
		{x509.KeyUsageDigitalSignature, "digitalSignature"},
		{x509.KeyUsageContentCommitment, "nonRepudiation"},
		{x509.KeyUsageKeyEncipherment, "keyEncipherment"},
		{x509.KeyUsageDataEncipherment, "dataEncipherment"},
		{x509.KeyUsageKeyAgreement, "keyAgreement"},
		{x509.KeyUsageCertSign, "keyCertSign"},
		{x509.KeyUsageCRLSign, "cRLSign"},
		{x509.KeyUsageEncipherOnly, "encipherOnly"},
		{x509.KeyUsageDecipherOnly, "decipherOnly"},
	}
	var out []string
	for _, m := range mapping {
		if ku&m.bit != 0 {
			out = append(out, m.name)
		}
	}
	return out
}
