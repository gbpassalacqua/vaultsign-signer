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
