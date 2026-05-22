package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// JsonRpcMessage is a standard JSON-RPC 2.0 message envelope.
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

// MCPToolRouter registers MCP clients and serves unified dynamic tool execution and prefix mapping.
type MCPToolRouter struct {
	mu      sync.RWMutex
	clients map[string]*MCPClient
}

// GlobalMCPRouter is the singleton tool router.
var GlobalMCPRouter = &MCPToolRouter{
	clients: make(map[string]*MCPClient),
}

// LoadAndStartPlugins reads plugins.json and launches MCP server backends.
func (r *MCPToolRouter) LoadAndStartPlugins() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	root := findProjectRoot(wd)
	cfgPath := filepath.Join(root, ".iroha", "plugins.json")
	oldCfgPath := filepath.Join(root, ".go-claude", "plugins.json")

	// Migration logic for local plugins
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		if oldData, oldErr := os.ReadFile(oldCfgPath); oldErr == nil {
			_ = os.MkdirAll(filepath.Dir(cfgPath), 0755)
			_ = os.WriteFile(cfgPath, oldData, 0644)
			_ = os.Rename(oldCfgPath, oldCfgPath+".bak")
		}
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		if os.IsNotExist(err) {
			defaultConfig := PluginsConfig{
				MCPServers: map[string]MCPServerConfig{
					"github": {
						Command: "npx",
						Args:    []string{"-y", "@modelcontextprotocol/server-github"},
						Env:     []string{"GITHUB_PERSONAL_ACCESS_TOKEN="},
					},
				},
			}
			data, _ = json.MarshalIndent(defaultConfig, "", "  ")
			_ = os.MkdirAll(filepath.Dir(cfgPath), 0755)
			if writeErr := os.WriteFile(cfgPath, data, 0644); writeErr != nil {
				return nil
			}
		} else {
			return err
		}
	}

	var config PluginsConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}

	for name, srvConfig := range config.MCPServers {
		if _, ok := r.clients[name]; ok {
			continue // Already started
		}
		client := NewMCPClient(name, srvConfig)
		if err := client.Start(); err != nil {
			// Skip or log failure
			continue
		}
		r.clients[name] = client
	}

	return nil
}

// ListServers returns the active connected plugin server statuses.
func (r *MCPToolRouter) ListServers() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := make(map[string]string)
	for name, c := range r.clients {
		status := "connected"
		if c.cmd == nil || c.cmd.Process == nil {
			status = "stopped"
		}
		res[name] = status
	}
	return res
}

// DiscoverTools queries all plugins for available tools and wraps them as ADK runnable tools.
func (r *MCPToolRouter) DiscoverTools() ([]tool.Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var tools []tool.Tool
	for name, client := range r.clients {
		resp, err := client.Call("tools/list", nil)
		if err != nil {
			continue
		}

		var listResult struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				InputSchema any    `json:"inputSchema"`
			} `json:"tools"`
		}

		if err := json.Unmarshal(resp.Result, &listResult); err != nil {
			continue
		}

		for _, t := range listResult.Tools {
			dynamicName := fmt.Sprintf("mcp__%s__%s", name, t.Name)
			tools = append(tools, &DynamicMCPTool{
				name:         dynamicName,
				description:  t.Description,
				schema:       t.InputSchema,
				client:       client,
				originalName: t.Name,
			})
		}
	}

	return tools, nil
}

// CloseAll terminates all running plugin server backends.
func (r *MCPToolRouter) CloseAll() {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, client := range r.clients {
		client.Close()
	}
	r.clients = make(map[string]*MCPClient)
}

// DynamicMCPTool adapts dynamic MCP tools into the shared adkRunnableTool interface.
type DynamicMCPTool struct {
	name         string
	description  string
	schema       any
	client       *MCPClient
	originalName string
}

func (t *DynamicMCPTool) Name() string {
	return t.name
}

func (t *DynamicMCPTool) Description() string {
	return t.description
}

func (t *DynamicMCPTool) IsLongRunning() bool {
	return false
}

func (t *DynamicMCPTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	callParams := map[string]any{
		"name":      t.originalName,
		"arguments": args,
	}

	resp, err := t.client.Call("tools/call", callParams)
	if err != nil {
		return nil, err
	}

	var toolResult map[string]any
	if err := json.Unmarshal(resp.Result, &toolResult); err != nil {
		return nil, fmt.Errorf("failed to parse mcp tool output: %w", err)
	}

	return toolResult, nil
}

func (t *DynamicMCPTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:                 t.name,
		Description:          t.description,
		ParametersJsonSchema: t.schema,
	}
}

func (t *DynamicMCPTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	if req.Config == nil {
		req.Config = &genai.GenerateContentConfig{}
	}
	req.Config.Tools = append(req.Config.Tools, &genai.Tool{
		FunctionDeclarations: []*genai.FunctionDeclaration{t.Declaration()},
	})
	return nil
}
