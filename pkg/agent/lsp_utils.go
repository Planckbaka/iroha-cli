package agent

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"iroha/pkg/config"
)
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
