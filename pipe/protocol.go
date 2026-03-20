package pipe

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
)

// Request represents an incoming RPC request from Claude Desktop.
// Uses the same length-prefixed JSON protocol as the Windows named pipe.
type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
	ID     interface{}     `json:"id,omitempty"`
}

// Response represents an outgoing RPC response to Claude Desktop.
// The TypeScript VM client (vZe) expects:
//
//	Success: {"success": true, "result": {...}}
//	Error:   {"success": false, "error": "message"}
type Response struct {
	Success bool        `json:"success"`
	Result  interface{} `json:"result,omitempty"`
	ID      interface{} `json:"id,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// ReadMessage reads a length-prefixed JSON message from the connection.
// Protocol: 4-byte big-endian length prefix followed by JSON payload.
func ReadMessage(conn net.Conn) ([]byte, error) {
	// Read 4-byte length prefix (big-endian)
	lenBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lenBuf); err != nil {
		return nil, fmt.Errorf("reading length prefix: %w", err)
	}

	length := binary.BigEndian.Uint32(lenBuf)
	if length == 0 {
		return nil, fmt.Errorf("zero-length message")
	}
	if length > 10*1024*1024 { // 10MB max message size
		return nil, fmt.Errorf("message too large: %d bytes", length)
	}

	// Read the JSON payload
	payload := make([]byte, length)
	if _, err := io.ReadFull(conn, payload); err != nil {
		return nil, fmt.Errorf("reading payload (%d bytes): %w", length, err)
	}

	return payload, nil
}

// WriteMessage writes a length-prefixed JSON message to the connection.
// Uses a single Write call to prevent interleaving with concurrent writers.
func WriteMessage(conn net.Conn, data []byte) error {
	buf := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(buf[:4], uint32(len(data)))
	copy(buf[4:], data)
	_, err := conn.Write(buf)
	return err
}

// WriteResponse serializes and sends a success Response.
// The id parameter must be the request ID so the client can match the response.
func WriteResponse(conn net.Conn, id interface{}, result interface{}) error {
	resp := Response{
		Success: true,
		ID:      id,
		Result:  result,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshaling response: %w", err)
	}
	return WriteMessage(conn, data)
}

// WriteError sends an error response.
func WriteError(conn net.Conn, id interface{}, code int, message string) error {
	resp := Response{
		Success: false,
		ID:      id,
		Error:   message,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("marshaling error response: %w", err)
	}
	return WriteMessage(conn, data)
}
