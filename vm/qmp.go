package vm

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

// QmpClient is a minimal QMP (QEMU Machine Protocol) client.
// QMP speaks newline-delimited JSON: after connecting the server emits a
// "QMP" greeting, then the client sends {"execute":"qmp_capabilities"} to
// leave Capabilities Negotiation. Subsequent commands are fire-and-forget
// per line, each producing a {"return":...} or {"error":...} reply.
type QmpClient struct {
	conn   net.Conn
	reader *bufio.Reader
	mu     sync.Mutex
}

// DialQMP connects to a QMP Unix socket, waits up to timeout for the socket
// to exist, and negotiates capabilities.
func DialQMP(socketPath string, timeout time.Duration) (*QmpClient, error) {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("QMP socket never appeared: %s", socketPath)
		}
		time.Sleep(200 * time.Millisecond)
	}

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("dial QMP: %w", err)
	}
	q := &QmpClient{conn: conn, reader: bufio.NewReader(conn)}
	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("setting QMP read deadline: %w", err)
	}

	// Wait for {"QMP":{...}} greeting.
	if _, err := q.readLine(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("reading QMP greeting: %w", err)
	}

	if _, err := q.Send(map[string]string{"execute": "qmp_capabilities"}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("qmp_capabilities: %w", err)
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("clearing QMP read deadline: %w", err)
	}
	return q, nil
}

// Send writes a JSON object and returns the parsed response.
func (q *QmpClient) Send(cmd interface{}) (map[string]json.RawMessage, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	data, err := json.Marshal(cmd)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')

	if err := q.conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return nil, fmt.Errorf("set QMP send deadline: %w", err)
	}
	defer func() { _ = q.conn.SetDeadline(time.Time{}) }()

	if _, err := q.conn.Write(data); err != nil {
		return nil, err
	}
	// QMP may emit "event" messages interleaved with replies. Keep reading
	// until we see a "return" or "error" key.
	for {
		line, err := q.readLine()
		if err != nil {
			return nil, err
		}
		var parsed map[string]json.RawMessage
		if err := json.Unmarshal(line, &parsed); err != nil {
			return nil, fmt.Errorf("QMP response JSON: %w", err)
		}
		if _, ok := parsed["return"]; ok {
			return parsed, nil
		}
		if _, ok := parsed["error"]; ok {
			return parsed, fmt.Errorf("QMP error: %s", string(parsed["error"]))
		}
		// otherwise it's an async event — ignore and read next line
	}
}

// Execute runs a QMP command without arguments.
func (q *QmpClient) Execute(command string) error {
	_, err := q.Send(map[string]string{"execute": command})
	return err
}

// Close closes the QMP connection.
func (q *QmpClient) Close() error {
	if q.conn == nil {
		return nil
	}
	err := q.conn.Close()
	q.conn = nil
	return err
}

func (q *QmpClient) readLine() ([]byte, error) {
	line, err := q.reader.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	return line, nil
}
