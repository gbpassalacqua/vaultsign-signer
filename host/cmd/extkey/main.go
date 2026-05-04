// Command extkey generates (or loads) an RSA-2048 keypair and prints
// the matching Chrome extension ID and base64-encoded SPKI public key
// for use in manifest.json's "key" field.
//
// Chrome derives the extension ID deterministically from SHA-256 of the
// SPKI DER, taking the first 16 bytes and mapping each hex digit value
// (0..15) to characters 'a'..'p'. Pinning the public key in manifest.json
// freezes the ID across dev (unpacked), CRX, and Web Store distributions
// so the native host's allowed_origins entry never goes stale.
//
//	go run ./cmd/extkey -key ../extension/extension-key.pem
//
// Outputs to stdout:
//
//	EXTENSION_ID: <32 chars a-p>
//	KEY: <base64 SPKI>
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"flag"
	"fmt"
	"os"
)

func main() {
	keyPath := flag.String("key", "extension-key.pem", "private key path (created if missing)")
	flag.Parse()

	priv, err := loadOrCreate(*keyPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal public key:", err)
		os.Exit(1)
	}

	sum := sha256.Sum256(pubDER)
	id := make([]byte, 32)
	for i, b := range sum[:16] {
		id[2*i] = 'a' + (b >> 4)
		id[2*i+1] = 'a' + (b & 0xf)
	}

	fmt.Printf("EXTENSION_ID: %s\n", id)
	fmt.Printf("KEY: %s\n", base64.StdEncoding.EncodeToString(pubDER))
}

func loadOrCreate(path string) (*rsa.PrivateKey, error) {
	if data, err := os.ReadFile(path); err == nil {
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, fmt.Errorf("could not parse PEM at %s", path)
		}
		if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
			fmt.Fprintln(os.Stderr, "loaded existing key:", path)
			return k, nil
		}
		k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse key: %w", err)
		}
		rk, ok := k.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("key is not RSA")
		}
		fmt.Fprintln(os.Stderr, "loaded existing key:", path)
		return rk, nil
	}

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	pemBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(pemBlock), 0o600); err != nil {
		return nil, fmt.Errorf("write key: %w", err)
	}
	fmt.Fprintln(os.Stderr, "generated new key:", path)
	return priv, nil
}
