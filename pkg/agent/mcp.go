package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// JsonRpcMessage is a standard JSON-RPC 2.0 message envelope.
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
			if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
				LogError(CatSystem, "mcp_migration", "Failed to create MCP plugins directory during migration", err, map[string]any{"path": cfgPath})
			} else {
				if err := os.WriteFile(cfgPath, oldData, 0644); err != nil {
					LogError(CatSystem, "mcp_migration", "Failed to migrate MCP plugins config", err, map[string]any{"from": oldCfgPath, "to": cfgPath})
				}
				if err := os.Rename(oldCfgPath, oldCfgPath+".bak"); err != nil {
					LogError(CatSystem, "mcp_migration", "Failed to rename old MCP plugins config", err, map[string]any{"path": oldCfgPath})
				}
			}
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

	// Scan active skill directories for local skill-level plugins.json
	uniqueSkillDirs := getUniqueSkillDirs(wd)
	for _, dir := range uniqueSkillDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, de := range entries {
			if !de.IsDir() {
				continue
			}

			// Try paths like skill_dir/skill_name/plugins.json
			paths := []string{
				filepath.Join(dir, de.Name(), "plugins.json"),
				filepath.Join(dir, de.Name(), ".iroha", "plugins.json"),
				filepath.Join(dir, de.Name(), ".go-claude", "plugins.json"),
			}
			for _, p := range paths {
				if skillData, err := os.ReadFile(p); err == nil {
					var skillCfg PluginsConfig
					if err := json.Unmarshal(skillData, &skillCfg); err == nil {
						for name, srvConfig := range skillCfg.MCPServers {
							// Unique name per skill to prevent conflict
							fullName := fmt.Sprintf("%s__%s", de.Name(), name)
							if _, ok := r.clients[fullName]; ok {
								continue // Already started
							}
							client := NewMCPClient(fullName, srvConfig)
							if err := client.Start(); err != nil {
								continue
							}
							r.clients[fullName] = client
						}
					}
					break // break the paths loop if we read one
				}
			}
		}
	}

	// Load plugin manifests from plugin directories and merge their MCP servers
	if err := GlobalPluginManager.LoadPlugins(); err == nil {
		pluginServers := GlobalPluginManager.MergeMCPServers()
		for name, srvConfig := range pluginServers {
			if _, ok := r.clients[name]; ok {
				continue
			}
			client := NewMCPClient(name, srvConfig)
			if err := client.Start(); err != nil {
				continue
			}
			r.clients[name] = client
		}

		// Merge plugin hooks into the global hook manager
		pluginHooks := GlobalPluginManager.MergeHooks()
		if len(pluginHooks) > 0 {
			GlobalHookManager.mergePluginHooks(pluginHooks)
		}
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
