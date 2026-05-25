package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestValidateManifest(t *testing.T) {
	tests := []struct {
		name     string
		manifest PluginManifest
		wantErr  bool
	}{
		{"Valid", PluginManifest{ID: "my-plugin", Name: "My Plugin", Version: "1.0.0"}, false},
		{"Empty ID", PluginManifest{ID: "", Name: "My Plugin"}, true},
		{"Invalid ID with slash", PluginManifest{ID: "my/plugin", Name: "My Plugin", Version: "1.0.0"}, true},
		{"Invalid ID with double underscore", PluginManifest{ID: "my__plugin", Name: "My Plugin", Version: "1.0.0"}, true},
		{"Empty Name", PluginManifest{ID: "my-plugin", Name: "", Version: "1.0.0"}, true},
		{"Invalid version", PluginManifest{ID: "my-plugin", Name: "My Plugin", Version: "not-semver"}, true},
		{"Missing version", PluginManifest{ID: "my-plugin", Name: "My Plugin"}, true},
		{"Valid with v prefix", PluginManifest{ID: "my-plugin", Name: "My Plugin", Version: "v1.2.3"}, false},
		{"Valid prerelease", PluginManifest{ID: "my-plugin", Name: "My Plugin", Version: "1.0.0-alpha"}, false},
		{"ID with underscores", PluginManifest{ID: "my_plugin", Name: "My Plugin", Version: "1.0.0"}, false},
		{"ID starting with digit", PluginManifest{ID: "0plugin", Name: "My Plugin", Version: "1.0.0"}, false},
		{"ID starting with hyphen", PluginManifest{ID: "-plugin", Name: "My Plugin", Version: "1.0.0"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateManifest(&tt.manifest)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateManifest() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPluginManagerGetPluginsAndGetByID(t *testing.T) {
	pm := &PluginManager{}

	// Manually set plugins on the manager for testing GetPlugins/GetPluginByID
	manifest := &PluginManifest{
		ID:      "test-plugin",
		Name:    "Test Plugin",
		Version: "1.0.0",
	}
	pm.mu.Lock()
	pm.plugins = append(pm.plugins, manifest)
	pm.mu.Unlock()

	plugins := pm.GetPlugins()
	if len(plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(plugins))
	}
	if plugins[0].ID != "test-plugin" {
		t.Errorf("expected plugin ID 'test-plugin', got %q", plugins[0].ID)
	}

	// Test GetPluginByID
	p := pm.GetPluginByID("test-plugin")
	if p == nil {
		t.Fatal("expected to find test-plugin")
	}
	if p.Name != "Test Plugin" {
		t.Errorf("expected name 'Test Plugin', got %q", p.Name)
	}

	// Test not found
	p = pm.GetPluginByID("nonexistent")
	if p != nil {
		t.Error("expected nil for nonexistent plugin")
	}
}

func TestDiscoverPluginsFromDir(t *testing.T) {
	// Create a temp plugin directory with manifest
	tmpDir := t.TempDir()
	pluginDir := filepath.Join(tmpDir, "test-plugin")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatal(err)
	}

	manifest := PluginManifest{
		ID:      "test-plugin",
		Name:    "Test Plugin",
		Version: "1.0.0",
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	// Discover from temp dir
	plugins, err := DiscoverPlugins(tmpDir)
	if err != nil {
		t.Fatalf("DiscoverPlugins() error = %v", err)
	}
	if len(plugins) != 1 {
		t.Fatalf("expected 1 plugin, got %d", len(plugins))
	}
	if plugins[0].ID != "test-plugin" {
		t.Errorf("expected plugin ID 'test-plugin', got %q", plugins[0].ID)
	}
}

func TestDiscoverPluginsEmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	plugins, err := DiscoverPlugins(tmpDir)
	if err != nil {
		t.Fatalf("DiscoverPlugins() error = %v", err)
	}
	if len(plugins) != 0 {
		t.Errorf("expected 0 plugins in empty dir, got %d", len(plugins))
	}
}

func TestDiscoverPluginsNonexistentDir(t *testing.T) {
	plugins, err := DiscoverPlugins("/nonexistent/path/12345")
	if err != nil {
		t.Fatalf("DiscoverPlugins() error = %v", err)
	}
	if len(plugins) != 0 {
		t.Errorf("expected nil for nonexistent dir, got %d plugins", len(plugins))
	}
}

func TestMigratePluginsConfig(t *testing.T) {
	result := MigratePluginsConfig(PluginsConfig{
		MCPServers: map[string]MCPServerConfig{
			"legacy-mcp": {Command: "node", Args: []string{"server.js"}},
		},
	})
	if result == nil {
		t.Fatal("expected non-nil migrated manifest")
	}
	if result.ID != "migrated-legacy" {
		t.Errorf("expected ID 'migrated-legacy', got %q", result.ID)
	}
	if result.Name != "Migrated Legacy Plugins" {
		t.Errorf("expected name 'Migrated Legacy Plugins', got %q", result.Name)
	}
	if len(result.MCPServers) != 1 {
		t.Errorf("expected 1 MCP server, got %d", len(result.MCPServers))
	}
}

func TestMigratePluginsConfigEmpty(t *testing.T) {
	result := MigratePluginsConfig(PluginsConfig{})
	if result != nil {
		t.Error("expected nil for empty config")
	}
}

func TestPluginManagerMergeMCPServers(t *testing.T) {
	pm := &PluginManager{}
	pm.mu.Lock()
	pm.plugins = []*PluginManifest{
		{
			ID:      "plug-a",
			Name:    "Plugin A",
			Version: "1.0.0",
			MCPServers: map[string]MCPServerConfig{
				"server1": {Command: "cmd1"},
			},
		},
	}
	pm.mu.Unlock()

	merged := pm.MergeMCPServers()
	if len(merged) != 1 {
		t.Fatalf("expected 1 merged server, got %d", len(merged))
	}
	key := "plug-a__server1"
	if _, ok := merged[key]; !ok {
		t.Errorf("expected key %q in merged servers", key)
	}
}

func TestPluginManagerMergeHooks(t *testing.T) {
	pm := &PluginManager{}
	pm.mu.Lock()
	pm.plugins = []*PluginManifest{
		{
			ID:      "hook-plug",
			Name:    "Hook Plugin",
			Version: "1.0.0",
			Hooks: map[string][]HookDef{
				"pre_commit": {{Command: "lint.sh"}},
			},
		},
	}
	pm.mu.Unlock()

	merged := pm.MergeHooks()
	if len(merged) != 1 {
		t.Fatalf("expected 1 hook event, got %d", len(merged))
	}
	hooks, ok := merged["pre_commit"]
	if !ok {
		t.Fatal("expected 'pre_commit' in merged hooks")
	}
	if len(hooks) != 1 || hooks[0].Command != "lint.sh" {
		t.Errorf("unexpected hooks content: %+v", hooks)
	}
}
