package websocket_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// Only the client uses coder/websocket. The raw server independently parses
// RFC 6455 frames and unmasks each byte, so two faulty maskAsm calls cannot
// cancel each other out in a client/server round trip.
func TestClientMaskAgainstRawFrames(t *testing.T) {
	var payloads [][]byte
	for _, size := range []int{1, 15, 16, 17, 127, 128, 129, 65539} {
		data := make([]byte, size+1)
		for i := range data {
			data[i] = byte(i*131 + 17)
		}
		payloads = append(payloads, data[1:])
	}
	done := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			done <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
		sum := sha1.Sum([]byte(r.Header.Get("Sec-WebSocket-Key") + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
		_, err = fmt.Fprintf(buffered, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", base64.StdEncoding.EncodeToString(sum[:]))
		if err == nil {
			err = buffered.Flush()
		}
		if err == nil {
			for i, want := range payloads {
				if err = readMaskedMessage(buffered.Reader, want); err != nil {
					err = fmt.Errorf("message %d: %w", i, err)
					break
				}
			}
		}
		done <- err
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	for _, data := range payloads {
		if err := conn.Write(ctx, websocket.MessageBinary, data); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

func readMaskedMessage(reader *bufio.Reader, want []byte) error {
	var message []byte
	first := true
	for {
		var header [2]byte
		if _, err := io.ReadFull(reader, header[:]); err != nil {
			return err
		}
		opcode := header[0] & 15
		if header[0]&0x70 != 0 || first && opcode != 2 || !first && opcode != 0 || header[1]&0x80 == 0 {
			return fmt.Errorf("invalid binary client frame header %x", header)
		}
		length := uint64(header[1] & 127)
		switch length {
		case 126:
			var extended [2]byte
			if _, err := io.ReadFull(reader, extended[:]); err != nil {
				return err
			}
			length = uint64(binary.BigEndian.Uint16(extended[:]))
		case 127:
			var extended [8]byte
			if _, err := io.ReadFull(reader, extended[:]); err != nil {
				return err
			}
			length = binary.BigEndian.Uint64(extended[:])
		}
		if length > uint64(len(want)-len(message)) {
			return fmt.Errorf("frame length %d exceeds remaining message", length)
		}
		var key [4]byte
		if _, err := io.ReadFull(reader, key[:]); err != nil {
			return err
		}
		frame := make([]byte, int(length))
		if _, err := io.ReadFull(reader, frame); err != nil {
			return err
		}
		for i := range frame {
			frame[i] ^= key[i%4]
		}
		message = append(message, frame...)
		first = false
		if header[0]&0x80 != 0 {
			if !bytes.Equal(message, want) {
				return fmt.Errorf("unmasked message differs: length %d, want %d", len(message), len(want))
			}
			return nil
		}
	}
}
