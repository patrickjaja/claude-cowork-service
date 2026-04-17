package pipe

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"

	"github.com/patrickjaja/claude-cowork-service/logx"
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
//	Success: {"success": true, "result": {...}, "id": <request-id>}
//	Error:   {"success": false, "error": "message", "id": <request-id>}
//
// The "id" field MUST echo back the request ID so the client can match
// responses to requests. Without it, responses are treated as "orphaned".
type Response struct {
	ID      interface{} `json:"id,omitempty"`
	Success bool        `json:"success"`
	Result  interface{} `json:"result,omitempty"`
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

// WriteResponse serializes and sends a success Response. Any failure to
// marshal or write is logged at debug level — callers have nothing useful
// to do with the error (the connection is already dead) so we swallow it
// here rather than asking every call site to wrap the call.
// The id parameter must be the request ID so the client can match the response.
func WriteResponse(conn net.Conn, id interface{}, result interface{}) {
	resp := Response{
		ID:      id,
		Success: true,
		Result:  result,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		logx.Debug("marshaling response (id=%v): %v", id, err)
		return
	}
	if err := WriteMessage(conn, data); err != nil {
		logx.Debug("writing response (id=%v): %v", id, err)
	}
}

// WriteError sends an error response. Errors are logged at debug level for
// the same reason as WriteResponse — the connection is already broken.
func WriteError(conn net.Conn, id interface{}, code int, message string) {
	resp := Response{
		ID:      id,
		Success: false,
		Error:   message,
	}
	data, err := json.Marshal(resp)
	if err != nil {
		logx.Debug("marshaling error response (id=%v): %v", id, err)
		return
	}
	if err := WriteMessage(conn, data); err != nil {
		logx.Debug("writing error response (id=%v): %v", id, err)
	}
}
