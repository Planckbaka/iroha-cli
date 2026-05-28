package agent

import (
	"encoding/json"
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

// lspFileConfig represents the structure of ~/.iroha/lsp.json
type lspFileConfig struct {
	Servers map[string]LSPServerConfig `json:"servers"`
}

// LSPGotoDefinitionArgs holds arguments for the lsp_goto_definition tool.
type LSPGotoDefinitionArgs struct {
	Path      string `json:"path" description:"Current file relative or absolute path"`
	Line      int    `json:"line" description:"1-indexed line number where the symbol is located"`
	Character int    `json:"character" description:"1-indexed character offset/column where the symbol is located"`
}

// LSPGotoDefinitionResult holds the result of the lsp_goto_definition tool.
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

// LSPFindReferencesArgs holds arguments for the lsp_find_references tool.
type LSPFindReferencesArgs struct {
	Path      string `json:"path" description:"Current file relative or absolute path"`
	Line      int    `json:"line" description:"1-indexed line number where the symbol is located"`
	Character int    `json:"character" description:"1-indexed character offset/column where the symbol is located"`
}

// LSPReferenceEntry holds a single reference location.
type LSPReferenceEntry struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Character   int    `json:"character"`
	LineContent string `json:"line_content"`
}

// LSPFindReferencesResult holds the result of the lsp_find_references tool.
type LSPFindReferencesResult struct {
	Success    bool                `json:"success"`
	References []LSPReferenceEntry `json:"references,omitempty"`
	Count      int                 `json:"count"`
}

// LSPDocumentSymbolsArgs holds arguments for the lsp_document_symbols tool.
type LSPDocumentSymbolsArgs struct {
	Path string `json:"path" description:"Target file relative or absolute path"`
}

// LSPDocumentSymbolsResult holds the result of the lsp_document_symbols tool.
type LSPDocumentSymbolsResult struct {
	Success bool         `json:"success"`
	Symbols []FlatSymbol `json:"symbols,omitempty"`
	Count   int          `json:"count"`
}

// LSPHoverArgs holds arguments for the lsp_hover tool.
type LSPHoverArgs struct {
	File   string `json:"file" description:"File path"`
	Line   int    `json:"line" description:"Line number (1-based)"`
	Column int    `json:"column" description:"Column offset (1-based)"`
}

// LSPHoverResult holds the result of the lsp_hover tool.
type LSPHoverResult struct {
	Content string `json:"content"`
}

// lspHoverContent models the MarkedString or MarkupContent from the LSP Hover response.
type lspHoverContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type lspHoverResponse struct {
	Contents lspHoverContents `json:"contents"`
	Range    *lspRange        `json:"range,omitempty"`
}

// lspHoverContents can be a string, MarkupContent, or array of MarkedString.
type lspHoverContents json.RawMessage

// LSPDiagnosticsArgs holds arguments for the lsp_diagnostics tool.
type LSPDiagnosticsArgs struct {
	File string `json:"file" description:"File path to check"`
}

// LSPDiagnosticsResult holds the result of the lsp_diagnostics tool.
type LSPDiagnosticsResult struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// Diagnostic represents a single diagnostic item.
type Diagnostic struct {
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Message  string `json:"message"`
	Severity string `json:"severity"` // "error", "warning", "info", "hint"
}

// lspDiagnosticItem models a single Diagnostic from the LSP response.
type lspDiagnosticItem struct {
	Range    lspRange        `json:"range"`
	Severity int             `json:"severity"`
	Message  string          `json:"message"`
	Source   string          `json:"source,omitempty"`
	Code     json.RawMessage `json:"code,omitempty"`
}

// lspFullDiagnosticResponse models the response from textDocument/diagnostic (pull diagnostics).
type lspFullDiagnosticResponse struct {
	Items []lspDiagnosticItem `json:"items"`
}
