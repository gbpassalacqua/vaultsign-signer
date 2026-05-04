// Package cms builds RFC 5652 CMS SignedData structures by direct ASN.1
// emission via encoding/asn1. We don't use a third-party PKCS#7 library
// because the standard ones (digitorus/pkcs7, mozilla pkcs7) all sign
// internally — there is no public API to inject a pre-computed signature.
//
// Day-2 scope: detached SignedData WITHOUT signedAttrs. The signature is
// taken to be applied directly to the message digest (RFC 5652 §5.4 — when
// signedAttrs is absent, the input to the signature algorithm IS the
// digest of the encapsulated content). signedAttrs / signing-certificate-v2
// for PAdES-B-B compliance get added when we wire this into @signpdf.
package cms

import (
	"crypto/x509"
	"encoding/asn1"
	"fmt"
	"math/big"
)

// OIDs used in CMS SignedData.
var (
	oidSignedData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidData       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidSHA256     = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidRSA        = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
)

// asn1NULL is the canonical encoding of ASN.1 NULL — `05 00`. Used as
// AlgorithmIdentifier.parameters for SHA-256 and RSA.
var asn1NULL = asn1.RawValue{Tag: asn1.TagNull, FullBytes: []byte{0x05, 0x00}}

type contentInfo struct {
	ContentType asn1.ObjectIdentifier
	// Content is [0] EXPLICIT ANY DEFINED BY contentType. We construct
	// the [0] wrapper manually via RawValue.Class/Tag/IsCompound/Bytes
	// rather than using the "explicit,tag:0" struct tag, because struct
	// tags interact with RawValue in ways that are easy to get wrong.
	Content asn1.RawValue
}

type signedData struct {
	Version          int
	DigestAlgorithms []algorithmIdentifier `asn1:"set"`
	EncapContent     encapContentInfo
	// Certificates is [0] IMPLICIT CertificateSet OPTIONAL. We always
	// include the signer cert. The IMPLICIT [0] wrapper replaces the SET
	// tag (0x31) with 0xA0; the inner Bytes must be the concatenation of
	// each cert's DER (each starts with 0x30 SEQUENCE).
	Certificates asn1.RawValue
	SignerInfos  []signerInfo `asn1:"set"`
}

type encapContentInfo struct {
	EContentType asn1.ObjectIdentifier
	// EContent is absent for detached signatures.
}

type algorithmIdentifier struct {
	Algorithm  asn1.ObjectIdentifier
	Parameters asn1.RawValue `asn1:"optional"`
}

type issuerAndSerialNumber struct {
	// Issuer is the cert's RawIssuer DER bytes — we pass them through
	// verbatim via RawValue.FullBytes to preserve the exact RDN encoding.
	Issuer       asn1.RawValue
	SerialNumber *big.Int
}

type signerInfo struct {
	Version            int
	SID                issuerAndSerialNumber
	DigestAlgorithm    algorithmIdentifier
	SignatureAlgorithm algorithmIdentifier
	Signature          []byte // OCTET STRING per CMS
}

// BuildDetached returns the DER encoding of a CMS ContentInfo wrapping
// SignedData with a single SignerInfo for the given certificate, with no
// signedAttrs and no eContent (detached). rawSig must be the big-endian
// PKCS#1 v1.5 RSA signature over the SHA-256 digest.
func BuildDetached(rawSig, certDER []byte) ([]byte, error) {
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, fmt.Errorf("parse cert: %w", err)
	}

	sha256Alg := algorithmIdentifier{Algorithm: oidSHA256, Parameters: asn1NULL}
	rsaAlg := algorithmIdentifier{Algorithm: oidRSA, Parameters: asn1NULL}

	sd := signedData{
		Version:          1,
		DigestAlgorithms: []algorithmIdentifier{sha256Alg},
		EncapContent:     encapContentInfo{EContentType: oidData},
		Certificates: asn1.RawValue{
			Class:      asn1.ClassContextSpecific,
			Tag:        0,
			IsCompound: true,
			Bytes:      certDER, // single cert; SET-OF wrapper is implied by [0] IMPLICIT
		},
		SignerInfos: []signerInfo{{
			Version: 1,
			SID: issuerAndSerialNumber{
				Issuer:       asn1.RawValue{FullBytes: cert.RawIssuer},
				SerialNumber: cert.SerialNumber,
			},
			DigestAlgorithm:    sha256Alg,
			SignatureAlgorithm: rsaAlg,
			Signature:          rawSig,
		}},
	}

	sdDER, err := asn1.Marshal(sd)
	if err != nil {
		return nil, fmt.Errorf("marshal signedData: %w", err)
	}

	ci := contentInfo{
		ContentType: oidSignedData,
		Content: asn1.RawValue{
			Class:      asn1.ClassContextSpecific,
			Tag:        0,
			IsCompound: true,
			Bytes:      sdDER,
		},
	}

	return asn1.Marshal(ci)
}
