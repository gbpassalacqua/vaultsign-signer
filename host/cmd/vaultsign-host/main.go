// Command vaultsign-host is the Chrome Native Messaging host that
// enumerates and (Day 2) signs with ICP-Brasil certificates.
//
// Reads length-prefixed JSON requests from stdin and writes responses to
// stdout. Logs go to stderr (Chrome captures and surfaces them when the
// extension is loaded with Developer mode → Inspect views: service worker).
package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/gbpassalacqua/vaultsign-signer/host/internal/certstore"
	"github.com/gbpassalacqua/vaultsign-signer/host/internal/cms"
	"github.com/gbpassalacqua/vaultsign-signer/host/internal/protocol"
)

const version = "0.1.6"

type request struct {
	Action string `json:"action"`
}

type signRequest struct {
	Action        string `json:"action"`
	Thumbprint    string `json:"thumbprint"`
	Hash          string `json:"hash"`          // base64 of 32-byte SHA-256
	HashAlgorithm string `json:"hashAlgorithm"` // "SHA-256"
}

func main() {
	// During dev we mirror logs to %TEMP%\vaultsign-host.log so the user
	// can tail it while Chrome owns stdin/stdout. Stderr stays connected
	// in case the binary is run from a terminal.
	logPath := filepath.Join(os.TempDir(), "vaultsign-host.log")
	logWriter := io.Writer(os.Stderr)
	if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		logWriter = io.MultiWriter(os.Stderr, f)
		defer f.Close()
	}
	log.SetOutput(logWriter)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("vaultsign-host %s starting (pid=%d, log=%s)", version, os.Getpid(), logPath)
	defer log.Printf("vaultsign-host exiting")

	for {
		raw, err := protocol.ReadMessage(os.Stdin)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return
			}
			log.Printf("ReadMessage: %v", err)
			return
		}

		var req request
		if err := json.Unmarshal(raw, &req); err != nil {
			log.Printf("recv: invalid JSON (%d bytes): %v", len(raw), err)
			writeError(os.Stdout, "", fmt.Sprintf("invalid JSON: %v", err))
			continue
		}
		log.Printf("recv action=%q (%d bytes)", req.Action, len(raw))

		switch req.Action {
		case "ping":
			writeResp(os.Stdout, map[string]any{
				"action":  "pong",
				"version": version,
			})

		case "listCertificates":
			certs, err := certstore.ListCertificates()
			if err != nil {
				writeError(os.Stdout, req.Action, err.Error())
				continue
			}
			if certs == nil {
				certs = []certstore.CertInfo{}
			}
			writeResp(os.Stdout, map[string]any{
				"action":       req.Action,
				"certificates": certs,
			})

		case "signHash":
			var sr signRequest
			if err := json.Unmarshal(raw, &sr); err != nil {
				writeError(os.Stdout, req.Action, fmt.Sprintf("invalid signHash request: %v", err))
				continue
			}
			if sr.HashAlgorithm != "" && sr.HashAlgorithm != "SHA-256" {
				writeError(os.Stdout, req.Action, fmt.Sprintf("unsupported hashAlgorithm %q (only SHA-256)", sr.HashAlgorithm))
				continue
			}
			hashBytes, err := base64.StdEncoding.DecodeString(sr.Hash)
			if err != nil {
				writeError(os.Stdout, req.Action, fmt.Sprintf("hash is not valid base64: %v", err))
				continue
			}
			rawSig, certDER, err := certstore.SignHash(sr.Thumbprint, hashBytes)
			if err != nil {
				writeError(os.Stdout, req.Action, err.Error())
				continue
			}
			pkcs7DER, err := cms.BuildDetached(rawSig, certDER)
			if err != nil {
				writeError(os.Stdout, req.Action, fmt.Sprintf("build CMS: %v", err))
				continue
			}
			writeResp(os.Stdout, map[string]any{
				"action":      req.Action,
				"signature":   base64.StdEncoding.EncodeToString(pkcs7DER),
				"certificate": base64.StdEncoding.EncodeToString(certDER),
			})

		default:
			writeError(os.Stdout, req.Action, "unknown action")
		}
	}
}

func writeResp(w io.Writer, v any) {
	if err := protocol.WriteMessage(w, v); err != nil {
		log.Printf("WriteMessage: %v", err)
	}
}

func writeError(w io.Writer, action, msg string) {
	writeResp(w, map[string]any{
		"action": action,
		"error":  msg,
	})
}
