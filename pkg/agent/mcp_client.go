package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

)
type JsonRpcMessage struct {
	Jsonrpc string          `json:"jsonrpc"`
	Id      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *JsonRpcError   `json:"error,omitempty"`
}

// JsonRpcError holds JSON-RPC structured error details.
type JsonRpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// MCPServerConfig defines the execution commands to spawn a server process.
type MCPServerConfig struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`
}

// PluginsConfig represents the serialized registry inside plugins.json.
type PluginsConfig struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}

// MCPClient handles stdio-based JSON-RPC 2.0 lifecycle over a child process.
type MCPClient struct {
	mu       sync.Mutex
	name     string
	config   MCPServerConfig
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	stderr   io.ReadCloser
	pending  map[int64]chan *JsonRpcMessage
	nextID   int64
	stopChan chan struct{}
}

// NewMCPClient creates an MCPClient instance.
func NewMCPClient(name string, config MCPServerConfig) *MCPClient {
	return &MCPClient{
		name:     name,
		config:   config,
		pending:  make(map[int64]chan *JsonRpcMessage),
		nextID:   1,
		stopChan: make(chan struct{}),
	}
}

// Start launches the background process and performs the MCP initialize handshake.
func (c *MCPClient) Start() error {
	c.cmd = exec.Command(c.config.Command, c.config.Args...)
	if len(c.config.Env) > 0 {
		c.cmd.Env = append(os.Environ(), c.config.Env...)
	}

	stdin, err := c.cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to open stdin pipe: %w", err)
	}
	c.stdin = stdin

	stdout, err := c.cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to open stdout pipe: %w", err)
	}
	c.stdout = stdout

	stderr, err := c.cmd.StderrPipe()
	if err == nil {
		c.stderr = stderr
		go func() {
			scanner := bufio.NewScanner(stderr)
			for scanner.Scan() {
				// Discard/log stderr output from server
			}
		}()
	}

	if err := c.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start process %s: %w", c.config.Command, err)
	}

	go c.readLoop()

	// MCP handshakes
	initParams := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "iroha-client",
			"version": "1.0.0",
		},
	}

	_, err = c.Call("initialize", initParams)
	if err != nil {
		c.Close()
		return fmt.Errorf("mcp initialize failed: %w", err)
	}

	err = c.SendNotification("notifications/initialized", nil)
	if err != nil {
		c.Close()
		return fmt.Errorf("mcp notifications/initialized failed: %w", err)
	}

	return nil
}

// Close terminates the child process and cleans up handles.
func (c *MCPClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	select {
	case <-c.stopChan:
		return
	default:
		close(c.stopChan)
	}

	if c.stdin != nil {
		_ = c.stdin.Close()
	}
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
		_ = c.cmd.Wait()
	}
}

// SendNotification sends a JSON-RPC notification (no ID) over stdio.
func (c *MCPClient) SendNotification(method string, params any) error {
	var paramsRaw json.RawMessage
	if params != nil {
		pData, err := json.Marshal(params)
		if err != nil {
			return err
		}
		paramsRaw = pData
	}

	msg := JsonRpcMessage{
		Jsonrpc: "2.0",
		Method:  method,
		Params:  paramsRaw,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.stdin == nil {
		return fmt.Errorf("client stdin is not open")
	}

	_, err = c.stdin.Write(append(data, '\n'))
	return err
}

// Call executes a JSON-RPC request-response transaction over stdio with a 10s timeout.
func (c *MCPClient) Call(method string, params any) (*JsonRpcMessage, error) {
	c.mu.Lock()
	id := c.nextID
	c.nextID++
	ch := make(chan *JsonRpcMessage, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	var paramsRaw json.RawMessage
	if params != nil {
		pData, err := json.Marshal(params)
		if err != nil {
			c.mu.Lock()
			delete(c.pending, id)
			c.mu.Unlock()
			return nil, err
		}
		paramsRaw = pData
	}

	msg := JsonRpcMessage{
		Jsonrpc: "2.0",
		Id:      id,
		Method:  method,
		Params:  paramsRaw,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	c.mu.Lock()
	if c.stdin == nil {
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("stdin is closed")
	}
	_, err = c.stdin.Write(append(data, '\n'))
	c.mu.Unlock()

	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("mcp error: %s (code %d)", resp.Error.Message, resp.Error.Code)
		}
		return resp, nil
	case <-time.After(10 * time.Second):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("mcp request timeout")
	}
}

func (c *MCPClient) readLoop() {
	scanner := bufio.NewScanner(c.stdout)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var msg JsonRpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}

		if msg.Id != nil {
			var idVal int64
			switch v := msg.Id.(type) {
			case float64:
				idVal = int64(v)
			case int64:
				idVal = v
			}

			c.mu.Lock()
			ch, ok := c.pending[idVal]
			if ok {
				delete(c.pending, idVal)
				c.mu.Unlock()
				ch <- &msg
			} else {
				c.mu.Unlock()
			}
		}
	}
}

