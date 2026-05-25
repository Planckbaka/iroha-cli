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

	"google.golang.org/adk/tool"
)

type jsonrpcRequest struct {
	Jsonrpc string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	Jsonrpc string          `json:"jsonrpc"`
	ID      *int64          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type lspPosition struct {
	Line      int `json:"line"`      // 0-indexed
	Character int `json:"character"` // 0-indexed
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspLocation struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type lspLocationLink struct {
	TargetURI   string   `json:"targetUri"`
	TargetRange lspRange `json:"targetRange"`
}

type lspDocumentSymbol struct {
	Name           string              `json:"name"`
	Kind           int                 `json:"kind"`
	Range          lspRange            `json:"range"`
	SelectionRange lspRange            `json:"selectionRange"`
	Children       []lspDocumentSymbol `json:"children,omitempty"`
}

type lspSymbolInformation struct {
	Name     string      `json:"name"`
	Kind     int         `json:"kind"`
	Location lspLocation `json:"location"`
}

type FlatSymbol struct {
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	StartLine int    `json:"start_line"` // 1-indexed
	EndLine   int    `json:"end_line"`   // 1-indexed
}

// LSPServerConfig defines a language server for a specific language.
type LSPServerConfig struct {
	Language     string   `json:"language"`
	Command      string   `json:"command"`
	Args         []string `json:"args,omitempty"`
	FilePatterns []string `json:"file_patterns,omitempty"`
}

// DefaultLSPServers provides built-in defaults for common languages.
var DefaultLSPServers = []LSPServerConfig{
	{Language: "go", Command: "gopls", Args: []string{"-mode=stdio"}, FilePatterns: []string{"*.go"}},
	{Language: "typescript", Command: "typescript-language-server", Args: []string{"--stdio"}, FilePatterns: []string{"*.ts", "*.tsx", "*.js", "*.jsx"}},
	{Language: "python", Command: "pyright-langserver", Args: []string{"--stdio"}, FilePatterns: []string{"*.py"}},
	{Language: "rust", Command: "rust-analyzer", Args: []string{}, FilePatterns: []string{"*.rs"}},
}

// lspServers holds the active server configurations, merged from defaults and user config.
var lspServers []LSPServerConfig

func init() {
	lspServers = make([]LSPServerConfig, len(DefaultLSPServers))
	copy(lspServers, DefaultLSPServers)
}

// SetLSPServers replaces the active LSP server list (called after loading user config).
func SetLSPServers(servers []LSPServerConfig) {
	// Build merged list: user servers first, then defaults not overridden.
	seenLang := make(map[string]bool)
	var merged []LSPServerConfig
	for _, s := range servers {
		merged = append(merged, s)
		seenLang[s.Language] = true
	}
	for _, s := range DefaultLSPServers {
		if !seenLang[s.Language] {
			merged = append(merged, s)
		}
	}
	lspServers = merged
}

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
	key := lspClientKey(workdir, language)
	lspClientsMu.Lock()
	client, exists := lspClients[key]
	if exists && !client.isClosed {
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

// ─── SWE Tools Handlers ──────────────────────────────────────────────────

type LSPGotoDefinitionArgs struct {
	Path      string `json:"path" description:"Current file relative or absolute path"`
	Line      int    `json:"line" description:"1-indexed line number where the symbol is located"`
	Character int    `json:"character" description:"1-indexed character offset/column where the symbol is located"`
}

type LSPGotoDefinitionResult struct {
	Success bool          `json:"success"`
	Message string        `json:"message"`
	Snippet string        `json:"snippet,omitempty"`
	File    string        `json:"file,omitempty"`
	Line    int           `json:"line,omitempty"`
	Range   *lspRangeView `json:"range,omitempty"`
}

type lspRangeView struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

func LSPGotoDefinitionHandler(ctx tool.Context, args LSPGotoDefinitionArgs) (LSPGotoDefinitionResult, error) {
	resolvedPath := resolvePath(ctx, args.Path)
	if err := validateSandboxPath(ctx, resolvedPath); err != nil {
		return LSPGotoDefinitionResult{Success: false}, err
	}

	workdir := getWorkdir(ctx)
	lang := languageFromPath(resolvedPath)
	if lang == "" {
		return LSPGotoDefinitionResult{Success: false, Message: "Could not detect language from file extension"}, nil
	}
	client, err := getLSPClient(workdir, lang)
	if err != nil {
		return LSPGotoDefinitionResult{Success: false}, WrapToolError("lsp_goto_definition", args, err)
	}

	params := map[string]any{
		"textDocument": map[string]any{
			"uri": pathToURI(resolvedPath),
		},
		"position": map[string]any{
			"line":      args.Line - 1,
			"character": args.Character - 1,
		},
	}

	resp, err := client.Call("textDocument/definition", params)
	if err != nil {
		return LSPGotoDefinitionResult{Success: false}, WrapToolError("lsp_goto_definition", args, err)
	}

	locs, err := parseLocations(resp.Result)
	if err != nil || len(locs) == 0 {
		return LSPGotoDefinitionResult{Success: false, Message: "Symbol definition not found"}, nil
	}

	targetFile := uriToPath(locs[0].URI)
	startL := locs[0].Range.Start.Line + 1
	endL := locs[0].Range.End.Line + 1

	relTarget, _ := filepath.Rel(workdir, targetFile)
	snippet := getSnippet(targetFile, startL, endL)

	return LSPGotoDefinitionResult{
		Success: true,
		Message: fmt.Sprintf("Symbol definition found: %s at line %d", relTarget, startL),
		File:    relTarget,
		Line:    startL,
		Snippet: snippet,
		Range: &lspRangeView{
			Start: lspPosition{Line: startL, Character: locs[0].Range.Start.Character + 1},
			End:   lspPosition{Line: endL, Character: locs[0].Range.End.Character + 1},
		},
	}, nil
}

type LSPFindReferencesArgs struct {
	Path      string `json:"path" description:"Current file relative or absolute path"`
	Line      int    `json:"line" description:"1-indexed line number where the symbol is located"`
	Character int    `json:"character" description:"1-indexed character offset/column where the symbol is located"`
}

type LSPReferenceEntry struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Character   int    `json:"character"`
	LineContent string `json:"line_content"`
}

type LSPFindReferencesResult struct {
	Success    bool                `json:"success"`
	References []LSPReferenceEntry `json:"references,omitempty"`
	Count      int                 `json:"count"`
}

func LSPFindReferencesHandler(ctx tool.Context, args LSPFindReferencesArgs) (LSPFindReferencesResult, error) {
	resolvedPath := resolvePath(ctx, args.Path)
	if err := validateSandboxPath(ctx, resolvedPath); err != nil {
		return LSPFindReferencesResult{Success: false}, err
	}

	workdir := getWorkdir(ctx)
	lang := languageFromPath(resolvedPath)
	if lang == "" {
		return LSPFindReferencesResult{Success: false}, WrapToolError("lsp_find_references", args, fmt.Errorf("could not detect language from file extension"))
	}
	client, err := getLSPClient(workdir, lang)
	if err != nil {
		return LSPFindReferencesResult{Success: false}, WrapToolError("lsp_find_references", args, err)
	}

	params := map[string]any{
		"textDocument": map[string]any{
			"uri": pathToURI(resolvedPath),
		},
		"position": map[string]any{
			"line":      args.Line - 1,
			"character": args.Character - 1,
		},
		"context": map[string]any{
			"includeDeclaration": true,
		},
	}

	resp, err := client.Call("textDocument/references", params)
	if err != nil {
		return LSPFindReferencesResult{Success: false}, WrapToolError("lsp_find_references", args, err)
	}

	locs, err := parseLocations(resp.Result)
	if err != nil {
		return LSPFindReferencesResult{Success: false}, WrapToolError("lsp_find_references", args, err)
	}

	var refs []LSPReferenceEntry
	for _, loc := range locs {
		filePath := uriToPath(loc.URI)
		relFile, _ := filepath.Rel(workdir, filePath)

		startL := loc.Range.Start.Line + 1
		startC := loc.Range.Start.Character + 1

		// Fetch the line content
		content := ""
		if data, err := os.ReadFile(filePath); err == nil {
			lines := strings.Split(string(data), "\n")
			if startL > 0 && startL <= len(lines) {
				content = strings.TrimSpace(lines[startL-1])
			}
		}

		refs = append(refs, LSPReferenceEntry{
			File:        relFile,
			Line:        startL,
			Character:   startC,
			LineContent: content,
		})
	}

	return LSPFindReferencesResult{
		Success:    true,
		References: refs,
		Count:      len(refs),
	}, nil
}

type LSPDocumentSymbolsArgs struct {
	Path string `json:"path" description:"Target file relative or absolute path"`
}

type LSPDocumentSymbolsResult struct {
	Success bool         `json:"success"`
	Symbols []FlatSymbol `json:"symbols,omitempty"`
	Count   int          `json:"count"`
}

func LSPDocumentSymbolsHandler(ctx tool.Context, args LSPDocumentSymbolsArgs) (LSPDocumentSymbolsResult, error) {
	resolvedPath := resolvePath(ctx, args.Path)
	if err := validateSandboxPath(ctx, resolvedPath); err != nil {
		return LSPDocumentSymbolsResult{Success: false}, err
	}

	workdir := getWorkdir(ctx)
	lang := languageFromPath(resolvedPath)
	if lang == "" {
		return LSPDocumentSymbolsResult{Success: false}, WrapToolError("lsp_document_symbols", args, fmt.Errorf("could not detect language from file extension"))
	}
	client, err := getLSPClient(workdir, lang)
	if err != nil {
		return LSPDocumentSymbolsResult{Success: false}, WrapToolError("lsp_document_symbols", args, err)
	}

	params := map[string]any{
		"textDocument": map[string]any{
			"uri": pathToURI(resolvedPath),
		},
	}

	resp, err := client.Call("textDocument/documentSymbol", params)
	if err != nil {
		return LSPDocumentSymbolsResult{Success: false}, WrapToolError("lsp_document_symbols", args, err)
	}

	var flatSymbols []FlatSymbol
	// Attempt parsing hierarchical DocumentSymbol list
	var docSymbols []lspDocumentSymbol
	if err := json.Unmarshal(resp.Result, &docSymbols); err == nil && len(docSymbols) > 0 && docSymbols[0].Name != "" {
		flatSymbols = flattenDocumentSymbols(docSymbols)
	} else {
		// Fallback to parsing flat SymbolInformation list
		var symInfos []lspSymbolInformation
		if err := json.Unmarshal(resp.Result, &symInfos); err == nil && len(symInfos) > 0 && symInfos[0].Name != "" {
			for _, s := range symInfos {
				flatSymbols = append(flatSymbols, FlatSymbol{
					Name:      s.Name,
					Kind:      symbolKindToString(s.Kind),
					StartLine: s.Location.Range.Start.Line + 1,
					EndLine:   s.Location.Range.End.Line + 1,
				})
			}
		}
	}

	return LSPDocumentSymbolsResult{
		Success: true,
		Symbols: flatSymbols,
		Count:   len(flatSymbols),
	}, nil
}

func flattenDocumentSymbols(symbols []lspDocumentSymbol) []FlatSymbol {
	var result []FlatSymbol
	var walk func(symbols []lspDocumentSymbol)
	walk = func(syms []lspDocumentSymbol) {
		for _, s := range syms {
			result = append(result, FlatSymbol{
				Name:      s.Name,
				Kind:      symbolKindToString(s.Kind),
				StartLine: s.Range.Start.Line + 1,
				EndLine:   s.Range.End.Line + 1,
			})
			if len(s.Children) > 0 {
				walk(s.Children)
			}
		}
	}
	walk(symbols)
	return result
}
