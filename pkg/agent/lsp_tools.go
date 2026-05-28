package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/adk/tool"
)

// ─── SWE Tools Handlers ──────────────────────────────────────────────────

func LSPGotoDefinitionHandler(ctx tool.Context, args LSPGotoDefinitionArgs) (LSPGotoDefinitionResult, error) {
	resolvedPath := resolvePath(ctx, args.Path)
	if err := validateSandboxPath(ctx, resolvedPath); err != nil {
		return LSPGotoDefinitionResult{Success: false}, err
	}

	workdir := getWorkdir(ctx)
	lang, langErr := languageFromPathOrError(resolvedPath)
	if langErr != nil {
		return LSPGotoDefinitionResult{Success: false, Message: langErr.Error()}, nil
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

func LSPFindReferencesHandler(ctx tool.Context, args LSPFindReferencesArgs) (LSPFindReferencesResult, error) {
	resolvedPath := resolvePath(ctx, args.Path)
	if err := validateSandboxPath(ctx, resolvedPath); err != nil {
		return LSPFindReferencesResult{Success: false}, err
	}

	workdir := getWorkdir(ctx)
	lang, langErr := languageFromPathOrError(resolvedPath)
	if langErr != nil {
		return LSPFindReferencesResult{Success: false}, WrapToolError("lsp_find_references", args, langErr)
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

func LSPDocumentSymbolsHandler(ctx tool.Context, args LSPDocumentSymbolsArgs) (LSPDocumentSymbolsResult, error) {
	resolvedPath := resolvePath(ctx, args.Path)
	if err := validateSandboxPath(ctx, resolvedPath); err != nil {
		return LSPDocumentSymbolsResult{Success: false}, err
	}

	workdir := getWorkdir(ctx)
	lang, langErr := languageFromPathOrError(resolvedPath)
	if langErr != nil {
		return LSPDocumentSymbolsResult{Success: false}, WrapToolError("lsp_document_symbols", args, langErr)
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

// ─── lsp_hover ──────────────────────────────────────────────────────────

func LSPHoverHandler(ctx tool.Context, args LSPHoverArgs) (LSPHoverResult, error) {
	resolvedPath := resolvePath(ctx, args.File)
	if err := validateSandboxPath(ctx, resolvedPath); err != nil {
		return LSPHoverResult{}, err
	}

	workdir := getWorkdir(ctx)
	lang, langErr := languageFromPathOrError(resolvedPath)
	if langErr != nil {
		return LSPHoverResult{}, WrapToolError("lsp_hover", args, langErr)
	}
	client, err := getLSPClient(workdir, lang)
	if err != nil {
		return LSPHoverResult{}, WrapToolError("lsp_hover", args, err)
	}

	params := map[string]any{
		"textDocument": map[string]any{
			"uri": pathToURI(resolvedPath),
		},
		"position": map[string]any{
			"line":      args.Line - 1,
			"character": args.Column - 1,
		},
	}

	resp, err := client.Call("textDocument/hover", params)
	if err != nil {
		return LSPHoverResult{}, WrapToolError("lsp_hover", args, err)
	}

	// A null result means no hover information available
	if len(resp.Result) == 0 || string(resp.Result) == "null" {
		return LSPHoverResult{Content: "No hover information available at this position."}, nil
	}

	var hover lspHoverResponse
	if err := json.Unmarshal(resp.Result, &hover); err != nil {
		// Try returning raw result as fallback
		return LSPHoverResult{Content: string(resp.Result)}, nil
	}

	content := formatHoverContents(json.RawMessage(hover.Contents))
	return LSPHoverResult{Content: content}, nil
}

// formatHoverContents extracts readable text from LSP hover contents,
// which can be a string, MarkupContent, or array of MarkedString.
func formatHoverContents(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "No hover information available."
	}

	// Try as plain string
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}

	// Try as MarkupContent {kind, value}
	var mc lspHoverContent
	if json.Unmarshal(raw, &mc) == nil && mc.Value != "" {
		return mc.Value
	}

	// Try as array of MarkedString
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		var parts []string
		for _, item := range arr {
			// Each item could be a string or a {language, value} object
			var str string
			if json.Unmarshal(item, &str) == nil {
				parts = append(parts, str)
				continue
			}
			var ms struct {
				Language string `json:"language"`
				Value    string `json:"value"`
			}
			if json.Unmarshal(item, &ms) == nil {
				parts = append(parts, ms.Value)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n\n")
		}
	}

	// Fallback: return raw
	return strings.TrimSpace(string(raw))
}

// ─── lsp_diagnostics ────────────────────────────────────────────────────

func LSPDiagnosticsHandler(ctx tool.Context, args LSPDiagnosticsArgs) (LSPDiagnosticsResult, error) {
	resolvedPath := resolvePath(ctx, args.File)
	if err := validateSandboxPath(ctx, resolvedPath); err != nil {
		return LSPDiagnosticsResult{}, err
	}

	workdir := getWorkdir(ctx)
	lang, langErr := languageFromPathOrError(resolvedPath)
	if langErr != nil {
		return LSPDiagnosticsResult{}, WrapToolError("lsp_diagnostics", args, langErr)
	}
	client, err := getLSPClient(workdir, lang)
	if err != nil {
		return LSPDiagnosticsResult{}, WrapToolError("lsp_diagnostics", args, err)
	}

	params := map[string]any{
		"textDocument": map[string]any{
			"uri": pathToURI(resolvedPath),
		},
	}

	// Try pull diagnostics first (textDocument/diagnostic, LSP 3.17+)
	resp, err := client.Call("textDocument/diagnostic", params)
	if err != nil {
		// If pull diagnostics is not supported, return empty with a note
		return LSPDiagnosticsResult{
			Diagnostics: []Diagnostic{},
		}, nil
	}

	if len(resp.Result) == 0 || string(resp.Result) == "null" {
		return LSPDiagnosticsResult{
			Diagnostics: []Diagnostic{},
		}, nil
	}

	var diagResp lspFullDiagnosticResponse
	if err := json.Unmarshal(resp.Result, &diagResp); err != nil {
		// Try as direct array (some servers return arrays directly)
		var items []lspDiagnosticItem
		if err2 := json.Unmarshal(resp.Result, &items); err2 != nil {
			return LSPDiagnosticsResult{
				Diagnostics: []Diagnostic{},
			}, nil
		}
		diagResp.Items = items
	}

	var diags []Diagnostic
	for _, d := range diagResp.Items {
		diags = append(diags, Diagnostic{
			Line:     d.Range.Start.Line + 1,
			Column:   d.Range.Start.Character + 1,
			Message:  d.Message,
			Severity: severityToString(d.Severity),
		})
	}

	return LSPDiagnosticsResult{
		Diagnostics: diags,
	}, nil
}

// severityToString converts an LSP DiagnosticSeverity number to a human-readable string.
func severityToString(severity int) string {
	switch severity {
	case 1:
		return "error"
	case 2:
		return "warning"
	case 3:
		return "info"
	case 4:
		return "hint"
	default:
		return "info"
	}
}
