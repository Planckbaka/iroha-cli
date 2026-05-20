package agent

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHelperProcess is a helper process that simulates a standard stdio JSON-RPC MCP server.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, `"initialize"`) {
			// Extract id
			id := "1"
			if strings.Contains(line, `"id":1`) {
				id = "1"
			}
			fmt.Printf(`{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"mock-server","version":"1.0.0"}}}`+"\n", id)
		} else if strings.Contains(line, `"tools/list"`) {
			fmt.Println(`{"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"echo","description":"echo text","inputSchema":{"type":"object"}}]}}`)
		} else if strings.Contains(line, `"tools/call"`) {
			fmt.Println(`{"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"hello mock"}],"isError":false}}`)
		}
	}
	os.Exit(0)
}

func TestMCPClient_Lifecycle(t *testing.T) {
	config := MCPServerConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess"},
		Env:     []string{"GO_WANT_HELPER_PROCESS=1"},
	}

	client := NewMCPClient("test-mcp", config)
	err := client.Start()
	if err != nil {
		t.Fatalf("failed to start client: %v", err)
	}
	defer client.Close()

	// Verify initialize handshake works and nextID progressed
	if client.nextID < 2 {
		t.Errorf("expected nextID to progress, got: %d", client.nextID)
	}

	// 1. Test tools/list
	resp, err := client.Call("tools/list", nil)
	if err != nil {
		t.Fatalf("failed to call tools/list: %v", err)
	}
	if !strings.Contains(string(resp.Result), `"echo"`) {
		t.Errorf("expected tools list to contain echo tool, got: %s", string(resp.Result))
	}

	// 2. Test tools/call
	respCall, err := client.Call("tools/call", map[string]any{
		"name":      "echo",
		"arguments": map[string]any{"text": "mocking"},
	})
	if err != nil {
		t.Fatalf("failed to call tools/call: %v", err)
	}
	if !strings.Contains(string(respCall.Result), "hello mock") {
		t.Errorf("expected hello mock response, got: %s", string(respCall.Result))
	}
}

func TestMCPToolRouter_DiscoveryAndRouting(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "go-claude-mcp-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Set up project root mock files
	goClaudeDir := filepath.Join(tempDir, ".go-claude")
	_ = os.MkdirAll(goClaudeDir, 0755)

	pluginsJson := fmt.Sprintf(`{
		"mcpServers": {
			"mock": {
				"command": "%s",
				"args": ["-test.run=TestHelperProcess"],
				"env": ["GO_WANT_HELPER_PROCESS=1"]
			}
		}
	}`, strings.ReplaceAll(os.Args[0], `\`, `\\`))

	err = os.WriteFile(filepath.Join(goClaudeDir, "plugins.json"), []byte(pluginsJson), 0644)
	if err != nil {
		t.Fatalf("failed to write plugins.json: %v", err)
	}

	// Override project root lookup by changing directory
	oldWd, _ := os.Getwd()
	_ = os.Chdir(tempDir)
	defer func() { _ = os.Chdir(oldWd) }()

	router := &MCPToolRouter{
		clients: make(map[string]*MCPClient),
	}
	defer router.CloseAll()

	err = router.LoadAndStartPlugins()
	if err != nil {
		t.Fatalf("failed to load and start plugins: %v", err)
	}

	// Wait for start handshake
	time.Sleep(100 * time.Millisecond)

	servers := router.ListServers()
	if status, ok := servers["mock"]; !ok || status != "connected" {
		t.Errorf("expected mock server to be connected, got: %v", servers)
	}

	tools, err := router.DiscoverTools()
	if err != nil {
		t.Fatalf("failed to discover tools: %v", err)
	}

	if len(tools) != 1 || tools[0].Name() != "mcp__mock__echo" {
		t.Errorf("unexpected discovered tools: %+v", tools)
	}

	// Test dynamic run
	runnable, ok := tools[0].(adkRunnableTool)
	if !ok {
		t.Fatalf("expected tool to implement adkRunnableTool")
	}

	res, err := runnable.Run(nil, map[string]any{"text": "hi"})
	if err != nil {
		t.Fatalf("failed to run dynamic tool: %v", err)
	}

	if !strings.Contains(fmt.Sprintf("%v", res), "hello mock") {
		t.Errorf("unexpected dynamic tool run result: %v", res)
	}
}
