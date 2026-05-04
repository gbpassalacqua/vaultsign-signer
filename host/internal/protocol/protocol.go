// Package protocol implements the Chrome Native Messaging framing:
// each message is a 4-byte little-endian uint32 length prefix followed
// by N bytes of UTF-8 JSON. https://developer.chrome.com/docs/extensions/develop/concepts/native-messaging
package protocol

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const maxMessageSize = 1 << 20 // 1 MiB — Chrome's limit for both directions

// ReadMessage reads one length-prefixed JSON message from r and returns
// the raw payload bytes. Returns io.EOF when the stream is closed cleanly
// at a frame boundary.
func ReadMessage(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.LittleEndian.Uint32(hdr[:])
	if n == 0 {
		return []byte{}, nil
	}
	if n > maxMessageSize {
		return nil, fmt.Errorf("message too large: %d bytes (max %d)", n, maxMessageSize)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("short read after length prefix: %w", err)
	}
	return buf, nil
}

// WriteMessage marshals v as JSON and writes it to w with a 4-byte
// little-endian length prefix.
func WriteMessage(w io.Writer, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if len(payload) > maxMessageSize {
		return errors.New("payload exceeds 1 MiB limit")
	}
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return nil
}
