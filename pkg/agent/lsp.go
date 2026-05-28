package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"


	"iroha/pkg/config"
)

// loadLSPConfig loads LSP server configuration from ~/.iroha/lsp.json.
// If the file does not exist or is invalid, defaults are preserved.
func loadLSPConfig() map[string]LSPServerConfig {
	defaults := map[string]LSPServerConfig{
		"go":         {Language: "go", Command: "gopls", Args: []string{"-mode=stdio"}, FilePatterns: []string{"*.go"}},
		"typescript": {Language: "typescript", Command: "typescript-language-server", Args: []string{"--stdio"}, FilePatterns: []string{"*.ts", "*.tsx", "*.js", "*.jsx"}},
		"python":     {Language: "python", Command: "pyright-langserver", Args: []string{"--stdio"}, FilePatterns: []string{"*.py"}},
		"rust":       {Language: "rust", Command: "rust-analyzer", Args: []string{}, FilePatterns: []string{"*.rs"}},
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return defaults
	}
	configPath := filepath.Join(home, ".iroha", "lsp.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return defaults
	}

	var fileCfg lspFileConfig
	if err := json.Unmarshal(data, &fileCfg); err != nil {
		return defaults
	}

	// Merge: user config overrides defaults
	for lang, serverCfg := range fileCfg.Servers {
		serverCfg.Language = lang
		defaults[lang] = serverCfg
	}
	return defaults
}

// LoadAndApplyLSPConfig loads LSP configuration from ~/.iroha/lsp.json and applies it.
func LoadAndApplyLSPConfig() {
	configs := loadLSPConfig()
	var servers []LSPServerConfig
	for _, cfg := range configs {
		servers = append(servers, cfg)
	}
	if len(servers) > 0 {
		SetLSPServers(servers)
	}
}

// lspIdleCleanupInterval is how often the idle cleanup goroutine checks for stale clients.
const lspIdleCleanupInterval = 1 * time.Minute

// lspIdleTimeout is how long a client must be unused before it is closed.
const lspIdleTimeout = 5 * time.Minute

// startLSPIdleCleanup starts a background goroutine (once) that periodically closes
// LSP clients unused for longer than lspIdleTimeout.
func startLSPIdleCleanup() {
	go func() {
		ticker := time.NewTicker(lspIdleCleanupInterval)
		defer ticker.Stop()
		for range ticker.C {
			lspClientsMu.Lock()
			now := time.Now()
			for key, client := range lspClients {
				client.mu.Lock()
				if !client.isClosed && now.Sub(client.lastUsed) > lspIdleTimeout {
					client.isClosed = true
					_ = client.stdin.Close()
					_ = client.stdout.Close()
					if client.cmd != nil && client.cmd.Process != nil {
						_ = client.cmd.Process.Kill()
					}
					for id, ch := range client.pending {
						close(ch)
						delete(client.pending, id)
					}
					delete(lspClients, key)
				}
				client.mu.Unlock()
			}
			lspClientsMu.Unlock()
		}
	}()
}

var lspIdleCleanupOnce sync.Once

// lspServerForLanguage returns the LSPServerConfig for a given language, or nil if not configured.
func lspServerForLanguage(lang string) *LSPServerConfig {
	for i := range lspServers {
		if lspServers[i].Language == lang {
			return &lspServers[i]
		}
	}
	return nil
}

// languageFromPath detects the language from a file extension.
func languageFromPath(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".go":
		return "go"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "typescript"
	case ".js":
		return "typescript"
	case ".jsx":
		return "typescript"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	default:
		return ""
	}
}

// languageFromPathOrError detects the language and returns a descriptive error
// including the file extension if no language is detected.
func languageFromPathOrError(filePath string) (string, error) {
	lang := languageFromPath(filePath)
	if lang == "" {
		ext := filepath.Ext(filePath)
		if ext == "" {
			return "", fmt.Errorf("no LSP server configured: file has no extension")
		}
		return "", fmt.Errorf("no LSP server configured for %s files", ext)
	}
	return lang, nil
}

type LSPClient struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	workdir  string
	language string
	reqID    int64
	pending  map[int64]chan *jsonrpcResponse
	isClosed bool
	lastUsed time.Time
}

var (
	lspClientsMu sync.Mutex
	lspClients   = make(map[string]*LSPClient) // key: "workdir:language"
)

// lspClientKey returns the cache key for a (workdir, language) pair.
func lspClientKey(workdir, language string) string {
	return workdir + ":" + language
}

func getLSPClient(workdir, language string) (*LSPClient, error) {
	// Ensure idle cleanup goroutine is started exactly once.
	lspIdleCleanupOnce.Do(startLSPIdleCleanup)

	key := lspClientKey(workdir, language)
	lspClientsMu.Lock()
	client, exists := lspClients[key]
	if exists && !client.isClosed {
		client.mu.Lock()
		client.lastUsed = time.Now()
		client.mu.Unlock()
		lspClientsMu.Unlock()
		return client, nil
	}

	cfg := lspServerForLanguage(language)
	if cfg == nil {
		lspClientsMu.Unlock()
		return nil, fmt.Errorf("no LSP server configured for language '%s'", language)
	}

	c, err := startLSPClient(workdir, cfg)
	if err != nil {
		lspClientsMu.Unlock()
		return nil, err
	}
	c.lastUsed = time.Now()
	lspClients[key] = c
	lspClientsMu.Unlock()
	return c, nil
}

func startLSPClient(workdir string, cfg *LSPServerConfig) (*LSPClient, error) {
	serverPath, err := exec.LookPath(cfg.Command)
	if err != nil {
		return nil, fmt.Errorf("'%s' binary not found in system PATH.\n[Fix suggestion] Install the %s language server ('%s') and retry.", cfg.Command, cfg.Language, cfg.Command)
	}

	cmd := exec.Command(serverPath, cfg.Args...)
	cmd.Dir = workdir

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}

	c := &LSPClient{
		cmd:      cmd,
		stdin:    stdin,
		stdout:   stdout,
		workdir:  workdir,
		language: cfg.Language,
		pending:  make(map[int64]chan *jsonrpcResponse),
	}

	go c.readLoop()

	if err := c.initialize(); err != nil {
		c.Close()
		return nil, err
	}

	return c, nil
}

func (c *LSPClient) readLoop() {
	reader := bufio.NewReader(c.stdout)
	for {
		var contentLength int
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				c.Close()
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if strings.HasPrefix(line, "Content-Length:") {
				parts := strings.Split(line, ":")
				if len(parts) == 2 {
					_, _ = fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &contentLength)
				}
			}
		}

		if contentLength <= 0 {
			continue
		}

		payload := make([]byte, contentLength)
		_, err := io.ReadFull(reader, payload)
		if err != nil {
			c.Close()
			return
		}

		var resp jsonrpcResponse
		if err := json.Unmarshal(payload, &resp); err != nil {
			continue
		}

		if resp.ID != nil {
			c.mu.Lock()
			ch, ok := c.pending[*resp.ID]
			if ok {
				delete(c.pending, *resp.ID)
				ch <- &resp
			}
			c.mu.Unlock()
		}
	}
}

func (c *LSPClient) Call(method string, params any) (*jsonrpcResponse, error) {
	c.mu.Lock()
	if c.isClosed {
		c.mu.Unlock()
		return nil, fmt.Errorf("LSP client is closed")
	}
	c.reqID++
	id := c.reqID
	ch := make(chan *jsonrpcResponse, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	req := jsonrpcRequest{
		Jsonrpc: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	payload, err := json.Marshal(req)
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	c.mu.Lock()
	_, err = fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n%s", len(payload), string(payload))
	c.mu.Unlock()
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case resp := <-ch:
		if resp == nil {
			return nil, fmt.Errorf("received nil response from loop")
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("LSP Error (%d): %s", resp.Error.Code, resp.Error.Message)
		}
		return resp, nil
	case <-time.After(15 * time.Second):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("LSP request timed out")
	}
}

func (c *LSPClient) Notify(method string, params any) error {
	c.mu.Lock()
	if c.isClosed {
		c.mu.Unlock()
		return fmt.Errorf("LSP client is closed")
	}
	c.mu.Unlock()

	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n%s", len(payload), string(payload))
	return err
}

func (c *LSPClient) initialize() error {
	absWorkdir, err := filepath.Abs(c.workdir)
	if err != nil {
		absWorkdir = c.workdir
	}
	rootURI := pathToURI(absWorkdir)

	params := map[string]any{
		"processId": os.Getpid(),
		"rootPath":  absWorkdir,
		"rootUri":   rootURI,
		"capabilities": map[string]any{
			"workspace": map[string]any{
				"workspaceFolders": true,
			},
			"textDocument": map[string]any{
				"definition": map[string]any{
					"dynamicRegistration": true,
				},
				"references": map[string]any{
					"dynamicRegistration": true,
				},
				"documentSymbol": map[string]any{
					"hierarchicalDocumentSymbolSupport": true,
				},
			},
		},
	}

	_, err = c.Call("initialize", params)
	if err != nil {
		return fmt.Errorf("LSP initialize failed: %w", err)
	}

	err = c.Notify("initialized", map[string]any{})
	if err != nil {
		return fmt.Errorf("LSP initialized notification failed: %w", err)
	}

	return nil
}

func (c *LSPClient) Close() {
	c.mu.Lock()
	if c.isClosed {
		c.mu.Unlock()
		return
	}
	c.isClosed = true
	_ = c.stdin.Close()
	_ = c.stdout.Close()
	if c.cmd != nil && c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
}

func pathToURI(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	// Correct file URI encoding
	u := &url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(abs),
	}
	return u.String()
}

func uriToPath(uriStr string) string {
	u, err := url.Parse(uriStr)
	if err != nil || u.Scheme != "file" {
		return uriStr
	}
	return filepath.FromSlash(u.Path)
}

func parseLocations(raw json.RawMessage) ([]lspLocation, error) {
	var single lspLocation
	if err := json.Unmarshal(raw, &single); err == nil && single.URI != "" {
		return []lspLocation{single}, nil
	}

	var list []lspLocation
	if err := json.Unmarshal(raw, &list); err == nil && len(list) > 0 && list[0].URI != "" {
		return list, nil
	}

	var links []lspLocationLink
	if err := json.Unmarshal(raw, &links); err == nil && len(links) > 0 && links[0].TargetURI != "" {
		var resolved []lspLocation
		for _, l := range links {
			resolved = append(resolved, lspLocation{
				URI:   l.TargetURI,
				Range: l.TargetRange,
			})
		}
		return resolved, nil
	}

	return nil, fmt.Errorf("failed to parse locations from response payload")
}

func getSnippet(filePath string, startLine, endLine int) string {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if startLine <= 0 || startLine > len(lines) {
		return ""
	}

	limit := startLine + 15
	if limit > len(lines) {
		limit = len(lines)
	}
	if limit > endLine {
		if endLine >= startLine {
			limit = endLine + 1
		}
	}
	if limit > len(lines) {
		limit = len(lines)
	}

	var sb strings.Builder
	for i := startLine - 1; i < limit; i++ {
		sb.WriteString(fmt.Sprintf("%d: %s\n", i+1, lines[i]))
	}
	return sb.String()
}

func symbolKindToString(kind int) string {
	kinds := map[int]string{
		1: "File", 2: "Module", 3: "Namespace", 4: "Package", 5: "Class",
		6: "Method", 7: "Property", 8: "Field", 9: "Constructor", 10: "Enum",
		11: "Interface", 12: "Function", 13: "Variable", 14: "Constant",
		15: "String", 16: "Number", 17: "Boolean", 18: "Array", 19: "Object",
		20: "Key", 21: "Null", 22: "EnumMember", 23: "Struct", 24: "Event",
		25: "Operator", 26: "TypeParameter",
	}
	if name, ok := kinds[kind]; ok {
		return name
	}
	return "Unknown"
}

func registerLSPTools(r *ToolRegistry) {
	// Lazily load user LSP server config once.
	lspConfigOnce.Do(func() {
		if cfg, err := config.LoadConfig(); err == nil && len(cfg.LSPServers) > 0 {
			servers := make([]LSPServerConfig, len(cfg.LSPServers))
			for i, s := range cfg.LSPServers {
				servers[i] = LSPServerConfig{
					Language:     s.Language,
					Command:      s.Command,
					Args:         s.Args,
					FilePatterns: s.FilePatterns,
				}
			}
			SetLSPServers(servers)
		}
	})

	register(r,"lsp_goto_definition", "Locate the declaration and definition of a symbol at a specific line and column position via LSP. Supports Go, TypeScript, Python, Rust, and other configured language servers. Returns the defining file path, line number, and code snippet preview.", LSPGotoDefinitionHandler)
	register(r,"lsp_find_references", "Find all references and usages of a symbol at a specific position across the workspace via LSP. Supports Go, TypeScript, Python, Rust, and other configured language servers.", LSPFindReferencesHandler)
	register(r,"lsp_document_symbols", "Extract and list all semantic symbols (classes, structs, methods, functions, variables, etc.) from a specified file via LSP. Supports Go, TypeScript, Python, Rust, and other configured language servers.", LSPDocumentSymbolsHandler)
	register(r,"lsp_hover", "Get type information and documentation at a specific position in a file via LSP. Returns hover content including type signatures, doc comments, and inferred types.", LSPHoverHandler)
	register(r,"lsp_diagnostics", "Get diagnostic errors and warnings for a file using the language server. Returns a list of issues with line, column, severity, and message. Uses pull diagnostics (LSP 3.17+); falls back to empty if the server does not support it.", LSPDiagnosticsHandler)
}
