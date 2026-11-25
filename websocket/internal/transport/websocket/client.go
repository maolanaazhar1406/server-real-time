package websocket

import (
	"bufio"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"time"
)

// Client is a lightweight RFC 6455 WebSocket client for integration tests and load testing.
type Client struct {
	raw        net.Conn
	reader     *bufio.Reader
	writer     *bufio.Writer
	useDeflate bool
}

// Dial connects to a WebSocket server at rawURL (e.g. "ws://127.0.0.1:8080/ws").
func Dial(rawURL string, enableCompression bool) (*Client, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout("tcp", u.Host, 5*time.Second)
	if err != nil {
		return nil, err
	}

	var keyBytes [16]byte
	if _, err := rand.Read(keyBytes[:]); err != nil {
		_ = conn.Close()
		return nil, err
	}
	secKey := computeAcceptKeyRaw(keyBytes[:])

	path := u.RequestURI()
	if path == "" {
		path = "/"
	}

	var req strings.Builder
	fmt.Fprintf(&req, "GET %s HTTP/1.1\r\n", path)
	fmt.Fprintf(&req, "Host: %s\r\n", u.Host)
	req.WriteString("Upgrade: websocket\r\n")
	req.WriteString("Connection: Upgrade\r\n")
	fmt.Fprintf(&req, "Sec-WebSocket-Key: %s\r\n", secKey)
	req.WriteString("Sec-WebSocket-Version: 13\r\n")
	if enableCompression {
		req.WriteString("Sec-WebSocket-Extensions: permessage-deflate; client_no_context_takeover\r\n")
	}
	req.WriteString("\r\n")

	if _, err := conn.Write([]byte(req.String())); err != nil {
		_ = conn.Close()
		return nil, err
	}

	br := bufio.NewReader(conn)
	bw := bufio.NewWriter(conn)

	statusLine, err := br.ReadString('\n')
	if err != nil || !strings.Contains(statusLine, "101") {
		_ = conn.Close()
		return nil, fmt.Errorf("websocket handshake failed: %q err=%v", statusLine, err)
	}

	negotiatedDeflate := false
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			break
		}
		if strings.Contains(strings.ToLower(trimmed), "permessage-deflate") {
			negotiatedDeflate = true
		}
	}

	return &Client{
		raw:        conn,
		reader:     br,
		writer:     bw,
		useDeflate: negotiatedDeflate,
	}, nil
}

// WriteMessage sends a masked RFC 6455 data frame.
func (c *Client) WriteMessage(binaryFrame bool, payload []byte) error {
	op := opText
	if binaryFrame {
		op = opBinary
	}
	return c.writeMasked(op, false, payload)
}

// WriteCompressed sends a permessage-deflate compressed RFC 6455 frame.
func (c *Client) WriteCompressed(binaryFrame bool, payload []byte) error {
	op := opText
	if binaryFrame {
		op = opBinary
	}
	compressed, err := deflatePayload(payload)
	if err != nil {
		return err
	}
	return c.writeMasked(op, true, compressed)
}

// ReadMessage reads the next data frame (automatically answering Ping frames with Pong).
func (c *Client) ReadMessage(timeout time.Duration) (byte, []byte, error) {
	for {
		if timeout > 0 {
			_ = c.raw.SetReadDeadline(time.Now().Add(timeout))
		}
		_, rsv1, op, payload, err := readWSFrame(c.reader, 16*1024*1024)
		if err != nil {
			return 0, nil, err
		}
		if op == opPing {
			_ = c.writeMasked(opPong, false, payload)
			continue
		}
		if op == opPong {
			continue
		}
		if op == opClose {
			return opClose, payload, io.EOF
		}
		if rsv1 && c.useDeflate {
			payload, err = inflatePayload(payload)
			if err != nil {
				return 0, nil, err
			}
		}
		return op, payload, nil
	}
}

func (c *Client) Close() error {
	_ = c.writeMasked(opClose, false, []byte{0x03, 0xe8})
	return c.raw.Close()
}

func (c *Client) writeMasked(opcode byte, rsv1 bool, payload []byte) error {
	b0 := byte(0x80) | (opcode & 0x0F)
	if rsv1 {
		b0 |= 0x40
	}
	if err := c.writer.WriteByte(b0); err != nil {
		return err
	}

	n := len(payload)
	switch {
	case n < 126:
		if err := c.writer.WriteByte(0x80 | byte(n)); err != nil {
			return err
		}
	case n <= 65535:
		if err := c.writer.WriteByte(0x80 | 126); err != nil {
			return err
		}
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(n))
		if _, err := c.writer.Write(ext[:]); err != nil {
			return err
		}
	default:
		if err := c.writer.WriteByte(0x80 | 127); err != nil {
			return err
		}
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		if _, err := c.writer.Write(ext[:]); err != nil {
			return err
		}
	}

	var maskKey [4]byte
	if _, err := rand.Read(maskKey[:]); err != nil {
		return err
	}
	if _, err := c.writer.Write(maskKey[:]); err != nil {
		return err
	}

	if n > 0 {
		masked := make([]byte, n)
		for i := range payload {
			masked[i] = payload[i] ^ maskKey[i%4]
		}
		if _, err := c.writer.Write(masked); err != nil {
			return err
		}
	}
	return c.writer.Flush()
}

func computeAcceptKeyRaw(b []byte) string {
	// Reuse base64 encoding directly via standard library
	const enc = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	_ = enc
	return strings.TrimRight(base64Encode(b), "")
}

func base64Encode(b []byte) string {
	const table = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	out := make([]byte, 0, ((len(b)+2)/3)*4)
	for i := 0; i < len(b); i += 3 {
		var v uint32
		rem := len(b) - i
		v |= uint32(b[i]) << 16
		if rem > 1 {
			v |= uint32(b[i+1]) << 8
		}
		if rem > 2 {
			v |= uint32(b[i+2])
		}
		out = append(out, table[(v>>18)&0x3F], table[(v>>12)&0x3F])
		if rem > 1 {
			out = append(out, table[(v>>6)&0x3F])
		} else {
			out = append(out, '=')
		}
		if rem > 2 {
			out = append(out, table[v&0x3F])
		} else {
			out = append(out, '=')
		}
	}
	return string(out)
}
