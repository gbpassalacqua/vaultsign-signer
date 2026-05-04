// Command framer wraps stdin/stdout in the Chrome Native Messaging
// length-prefixed JSON envelope so vaultsign-host can be exercised from
// the terminal.
//
//   framer encode '<json>'   prints 4-byte LE length + raw json (binary)
//   framer encode -          reads stdin, emits the framed bytes
//   framer decode            reads framed bytes from stdin, prints JSON lines
//
// Typical pipeline:
//
//   framer encode '{"action":"listCertificates"}' | vaultsign-host.exe | framer decode
package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "encode":
		if err := runEncode(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "encode:", err)
			os.Exit(1)
		}
	case "decode":
		if err := runDecode(); err != nil {
			fmt.Fprintln(os.Stderr, "decode:", err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(2)
	}
}

func runEncode(args []string) error {
	var payload []byte
	if len(args) == 0 || args[0] == "-" {
		var err error
		payload, err = io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
	} else {
		payload = []byte(args[0])
	}
	// Sanity-check JSON so a typo doesn't blow up the host with a confusing
	// length prefix.
	var probe any
	if err := json.Unmarshal(payload, &probe); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := os.Stdout.Write(hdr[:]); err != nil {
		return err
	}
	_, err := os.Stdout.Write(payload)
	return err
}

func runDecode() error {
	for {
		var hdr [4]byte
		if _, err := io.ReadFull(os.Stdin, hdr[:]); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		n := binary.LittleEndian.Uint32(hdr[:])
		if n == 0 {
			fmt.Println("{}")
			continue
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(os.Stdin, buf); err != nil {
			return fmt.Errorf("short read: %w", err)
		}
		// Pretty-print so the output is readable in the terminal.
		var pretty any
		if err := json.Unmarshal(buf, &pretty); err != nil {
			os.Stdout.Write(buf)
			fmt.Println()
			continue
		}
		out, _ := json.MarshalIndent(pretty, "", "  ")
		os.Stdout.Write(out)
		fmt.Println()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: framer encode '<json>' | framer encode - | framer decode")
}
