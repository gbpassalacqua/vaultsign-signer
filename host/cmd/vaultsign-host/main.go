// Command vaultsign-host is the Chrome Native Messaging host that
// enumerates and (Day 2) signs with ICP-Brasil certificates.
//
// Reads length-prefixed JSON requests from stdin and writes responses to
// stdout. Logs go to stderr (Chrome captures and surfaces them when the
// extension is loaded with Developer mode → Inspect views: service worker).
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/gbpassalacqua/vaultsign-signer/host/internal/certstore"
	"github.com/gbpassalacqua/vaultsign-signer/host/internal/protocol"
)

const version = "0.1.0"

type request struct {
	Action string `json:"action"`
}

func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(log.Ltime | log.Lmicroseconds)

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
			writeError(os.Stdout, "", fmt.Sprintf("invalid JSON: %v", err))
			continue
		}

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
			// Day 2.
			writeError(os.Stdout, req.Action, "signHash not yet implemented")

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
