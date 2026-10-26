package quota

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const maxLineBytes = 4 * 1024 * 1024

// rpcConn speaks newline-delimited JSON-RPC over a child's stdio.
type rpcConn struct {
	stdin io.Writer
	lines *bufio.Scanner
}

func newRPCConn(stdin io.Writer, stdout io.Reader) *rpcConn {
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), maxLineBytes)
	return &rpcConn{stdin: stdin, lines: scanner}
}

func (c *rpcConn) send(message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return err
	}
	_, err = c.stdin.Write(append(data, '\n'))
	return err
}

// await returns the raw result for id, skipping notifications and politely
// rejecting server-initiated requests that CarryOn does not implement.
func (c *rpcConn) await(id int) (json.RawMessage, error) {
	for c.lines.Scan() {
		line := c.lines.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var envelope struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil {
			continue
		}
		if envelope.Method != "" && len(envelope.ID) > 0 {
			c.rejectServerRequest(envelope.ID)
			continue
		}
		if !idMatches(envelope.ID, id) {
			continue
		}
		if envelope.Error != nil {
			return nil, fmt.Errorf("rpc %d: %s", id, envelope.Error.Message)
		}
		return envelope.Result, nil
	}
	if err := c.lines.Err(); err != nil {
		return nil, fmt.Errorf("read protocol stream: %w", err)
	}
	return nil, errors.New("process exited before answering")
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *rpcConn) rejectServerRequest(id json.RawMessage) {
	_ = c.send(map[string]any{
		"id":    id,
		"error": map[string]any{"code": -32601, "message": "unsupported client request"},
	})
}

func idMatches(raw json.RawMessage, want int) bool {
	if len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	return value == want
}
