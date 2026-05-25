package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// PluginManifest defines the structure for a plugin.json manifest file.
type PluginManifest struct {
	ID          string                     `json:"id"`
	Name        string                     `json:"name"`
	Version     string                     `json:"version"`
	Description string                     `json:"description,omitempty"`
	MCPServers  map[string]MCPServerConfig `json:"mcp_servers,omitempty"`
	Hooks       map[string][]HookDef       `json:"hooks,omitempty"`
	Skills      []string                   `json:"skills,omitempty"`
	Permissions []string                   `json:"permissions,omitempty"`
}

// semverRe matches basic semver patterns like 1.0.0, 1.0.0-alpha, 1.0.0+build.
var semverRe = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-[a-zA-Z0-9.]+)?(\+[a-zA-Z0-9.]+)?$`)

// pluginIDRe validates plugin IDs: alphanumeric with hyphens/underscores, max 64 chars.
var pluginIDRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// PluginManager discovers, loads, and validates plugin manifests.
type PluginManager struct {
	mu      sync.RWMutex
	plugins []*PluginManifest
	sources []string // directories scanned for plugins
}

// GlobalPluginManager is the singleton plugin manager.
var GlobalPluginManager = &PluginManager{}

// LoadPluginManifest reads and validates a plugin.json file from disk.
func LoadPluginManifest(path string) (*PluginManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read plugin manifest %s: %w", path, err)
	}

	var manifest PluginManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse plugin manifest %s: %w", path, err)
	}

	if err := ValidateManifest(&manifest); err != nil {
		return nil, fmt.Errorf("invalid plugin manifest %s: %w", path, err)
	}

	return &manifest, nil
}

// ValidateManifest checks required fields and semver format.
func ValidateManifest(m *PluginManifest) error {
	if strings.TrimSpace(m.ID) == "" {
		return fmt.Errorf("plugin manifest missing required field: id")
	}
	if !pluginIDRe.MatchString(m.ID) {
		return fmt.Errorf("plugin ID %q must be alphanumeric with hyphens/underscores, max 64 chars", m.ID)
	}
	if strings.Contains(m.ID, "__") {
		return fmt.Errorf("plugin ID %q must not contain double underscores", m.ID)
	}
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("plugin manifest missing required field: name")
	}
	if strings.TrimSpace(m.Version) == "" {
		return fmt.Errorf("plugin manifest missing required field: version")
	}
	if !semverRe.MatchString(m.Version) {
		return fmt.Errorf("plugin manifest version %q is not valid semver (expected X.Y.Z)", m.Version)
	}
	return nil
}

// MigratePluginsConfig converts a flat PluginsConfig into a PluginManifest.
// The generated manifest uses "migrated" as the ID prefix.
func MigratePluginsConfig(old PluginsConfig) *PluginManifest {
	if len(old.MCPServers) == 0 {
		return nil
	}
	return &PluginManifest{
		ID:         "migrated-legacy",
		Name:       "Migrated Legacy Plugins",
		Version:    "1.0.0",
		MCPServers: old.MCPServers,
	}
}

// DiscoverPlugins scans a directory for subdirectories containing plugin.json files.
func DiscoverPlugins(dir string) ([]*PluginManifest, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to scan plugin directory %s: %w", dir, err)
	}

	var manifests []*PluginManifest
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		manifestPath := filepath.Join(dir, entry.Name(), "plugin.json")
		m, err := LoadPluginManifest(manifestPath)
		if err != nil {
			LogWarn(CatSystem, "plugin_discovery_skip", fmt.Sprintf("Skipping plugin: %v", err), map[string]any{
				"path": manifestPath,
			})
			continue
		}
		manifests = append(manifests, m)
		LogInfo(CatSystem, "plugin_discovered", fmt.Sprintf("Discovered plugin: %s (%s)", m.Name, m.ID), map[string]any{
			"id":      m.ID,
			"name":    m.Name,
			"version": m.Version,
		})
	}
	return manifests, nil
}

// LoadPlugins scans global and project plugin directories and caches results.
func (pm *PluginManager) LoadPlugins() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	pm.plugins = nil
	pm.sources = nil

	var allPlugins []*PluginManifest

	// Global plugins: ~/.iroha/plugins/*/plugin.json
	if home, err := os.UserHomeDir(); err == nil {
		globalDir := filepath.Join(home, ".iroha", "plugins")
		if manifests, err := DiscoverPlugins(globalDir); err == nil && len(manifests) > 0 {
			allPlugins = append(allPlugins, manifests...)
			pm.sources = append(pm.sources, globalDir)
		}
	}

	// Project plugins: .iroha/plugins/*/plugin.json
	if wd, err := os.Getwd(); err == nil {
		root := findProjectRoot(wd)
		projectDir := filepath.Join(root, ".iroha", "plugins")
		if manifests, err := DiscoverPlugins(projectDir); err == nil && len(manifests) > 0 {
			allPlugins = append(allPlugins, manifests...)
			pm.sources = append(pm.sources, projectDir)
		}
	}

	pm.plugins = allPlugins
	LogInfo(CatSystem, "plugins_loaded", fmt.Sprintf("Loaded %d plugin manifests", len(allPlugins)), map[string]any{
		"count": len(allPlugins),
	})
	return nil
}

// GetPlugins returns a copy of loaded plugin manifests.
func (pm *PluginManager) GetPlugins() []*PluginManifest {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	out := make([]*PluginManifest, len(pm.plugins))
	copy(out, pm.plugins)
	return out
}

// GetPluginByID finds a loaded plugin by its ID.
func (pm *PluginManager) GetPluginByID(id string) *PluginManifest {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	for _, p := range pm.plugins {
		if p.ID == id {
			return p
		}
	}
	return nil
}

// MergeMCPServers returns a merged map of MCP server configs from all loaded plugins.
func (pm *PluginManager) MergeMCPServers() map[string]MCPServerConfig {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	merged := make(map[string]MCPServerConfig)
	for _, p := range pm.plugins {
		for name, cfg := range p.MCPServers {
			fullName := fmt.Sprintf("%s__%s", p.ID, name)
			merged[fullName] = cfg
		}
	}
	return merged
}

// MergeHooks returns a merged map of hook definitions from all loaded plugins.
func (pm *PluginManager) MergeHooks() map[string][]HookDef {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	merged := make(map[string][]HookDef)
	for _, p := range pm.plugins {
		for event, defs := range p.Hooks {
			merged[event] = append(merged[event], defs...)
		}
	}
	return merged
}
