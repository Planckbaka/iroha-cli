package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool/toolconfirmation"
	"google.golang.org/genai"
)

// mockToolContext fully implements the tool.Context interface.
type mockToolContext struct {
	context.Context
}

func (m *mockToolContext) Actions() *session.EventActions { return nil }
func (m *mockToolContext) AgentName() string              { return "" }
func (m *mockToolContext) AppName() string                { return "" }
func (m *mockToolContext) Artifacts() adkagent.Artifacts  { return nil }
func (m *mockToolContext) Branch() string                 { return "" }
func (m *mockToolContext) FunctionCallID() string         { return "" }
func (m *mockToolContext) InvocationID() string           { return "" }
func (m *mockToolContext) ReadonlyState() session.ReadonlyState {
	return nil
}
func (m *mockToolContext) RequestConfirmation(prompt string, data interface{}) error {
	return nil
}
func (m *mockToolContext) SearchMemory(ctx context.Context, query string) (*memory.SearchResponse, error) {
	return nil, nil
}
func (m *mockToolContext) SessionID() string                                    { return "" }
func (m *mockToolContext) State() session.State                                 { return nil }
func (m *mockToolContext) ToolConfirmation() *toolconfirmation.ToolConfirmation { return nil }
func (m *mockToolContext) UserContent() *genai.Content                          { return nil }
func (m *mockToolContext) UserID() string                                       { return "" }

// mockLSPServer simulates the gopls JSON-RPC 2.0 stdio behavior.
type mockLSPServer struct {
	reader io.ReadCloser
	writer io.WriteCloser
	t      *testing.T
}

func (s *mockLSPServer) run() {
	defer s.reader.Close()
	defer s.writer.Close()

	br := bufio.NewReader(s.reader)
	for {
		var contentLength int
		for {
			line, err := br.ReadString('\n')
			if err != nil {
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
		_, err := io.ReadFull(br, payload)
		if err != nil {
			return
		}

		var req struct {
			Jsonrpc string          `json:"jsonrpc"`
			ID      int64           `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(payload, &req); err != nil {
			s.t.Logf("mock server unmarshal request error: %v", err)
			continue
		}

		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"capabilities": map[string]any{
					"definitionProvider": true,
				},
			}
		case "textDocument/definition":
			result = []lspLocation{
				{
					URI: pathToURI(filepath.Join(getWorkdir(context.TODO()), "dummy.go")),
					Range: lspRange{
						Start: lspPosition{Line: 4, Character: 1},
						End:   lspPosition{Line: 4, Character: 10},
					},
				},
			}
		case "textDocument/references":
			result = []lspLocation{
				{
					URI: pathToURI(filepath.Join(getWorkdir(context.TODO()), "dummy.go")),
					Range: lspRange{
						Start: lspPosition{Line: 4, Character: 1},
						End:   lspPosition{Line: 4, Character: 10},
					},
				},
			}
		case "textDocument/documentSymbol":
			result = []lspDocumentSymbol{
				{
					Name: "MyStruct",
					Kind: 23, // Struct
					Range: lspRange{
						Start: lspPosition{Line: 0, Character: 0},
						End:   lspPosition{Line: 10, Character: 0},
					},
					Children: []lspDocumentSymbol{
						{
							Name: "MyField",
							Kind: 8, // Field
							Range: lspRange{
								Start: lspPosition{Line: 2, Character: 1},
								End:   lspPosition{Line: 2, Character: 10},
							},
						},
					},
				},
			}
		default:
			result = map[string]any{}
		}

		resBytes, err := json.Marshal(result)
		if err != nil {
			s.t.Logf("mock server marshal result error: %v", err)
			continue
		}

		resp := jsonrpcResponse{
			Jsonrpc: "2.0",
			ID:      &req.ID,
			Result:  resBytes,
		}
		respPayload, err := json.Marshal(resp)
		if err != nil {
			s.t.Logf("mock server marshal response error: %v", err)
			continue
		}

		_, err = fmt.Fprintf(s.writer, "Content-Length: %d\r\n\r\n%s", len(respPayload), string(respPayload))
		if err != nil {
			s.t.Logf("mock server write response error: %v", err)
			return
		}
	}
}

func setupMockLSPClient(t *testing.T, workdir string) (*LSPClient, *mockLSPServer) {
	return setupMockLSPClientForLang(t, workdir, "go")
}

func setupMockLSPClientForLang(t *testing.T, workdir, language string) (*LSPClient, *mockLSPServer) {
	inReader, inWriter := io.Pipe()
	outReader, outWriter := io.Pipe()

	client := &LSPClient{
		stdin:    inWriter,
		stdout:   outReader,
		workdir:  workdir,
		language: language,
		pending:  make(map[int64]chan *jsonrpcResponse),
	}

	server := &mockLSPServer{
		reader: inReader,
		writer: outWriter,
		t:      t,
	}

	go client.readLoop()
	go server.run()

	key := lspClientKey(workdir, language)
	lspClientsMu.Lock()
	lspClients[key] = client
	lspClientsMu.Unlock()

	return client, server
}

func TestLSP_PathURIConversion(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "iroha-lsp-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	path := filepath.Join(tempDir, "file.go")
	uri := pathToURI(path)

	if !strings.HasPrefix(uri, "file://") {
		t.Errorf("expected URI to start with file://, got: %s", uri)
	}

	resolvedPath := uriToPath(uri)
	if filepath.Clean(resolvedPath) != filepath.Clean(path) {
		t.Errorf("expected path %q, got: %q", path, resolvedPath)
	}
}

func TestLSP_SymbolKindToString(t *testing.T) {
	tests := []struct {
		kind int
		want string
	}{
		{1, "File"},
		{6, "Method"},
		{23, "Struct"},
		{99, "Unknown"},
	}

	for _, tt := range tests {
		got := symbolKindToString(tt.kind)
		if got != tt.want {
			t.Errorf("symbolKindToString(%d) = %q, want %q", tt.kind, got, tt.want)
		}
	}
}

func TestLSP_GetSnippet(t *testing.T) {
	tempFile, err := os.CreateTemp("", "iroha-snippet-*.go")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tempFile.Name())

	content := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"Hello\")\n}\n"
	if _, err := tempFile.WriteString(content); err != nil {
		t.Fatal(err)
	}
	_ = tempFile.Close()

	snippet := getSnippet(tempFile.Name(), 5, 7)
	expectedLines := []string{
		"5: func main() {",
		"6: \tfmt.Println(\"Hello\")",
		"7: }",
	}

	for _, line := range expectedLines {
		if !strings.Contains(snippet, line) {
			t.Errorf("expected snippet to contain %q, got:\n%s", line, snippet)
		}
	}

	emptySnippet := getSnippet(tempFile.Name(), 100, 110)
	if emptySnippet != "" {
		t.Errorf("expected empty snippet for out-of-range lines, got: %q", emptySnippet)
	}
}

func TestLSP_FlattenDocumentSymbols(t *testing.T) {
	symbols := []lspDocumentSymbol{
		{
			Name: "Parent",
			Kind: 5,
			Range: lspRange{
				Start: lspPosition{Line: 0, Character: 0},
				End:   lspPosition{Line: 10, Character: 0},
			},
			Children: []lspDocumentSymbol{
				{
					Name: "Child",
					Kind: 6,
					Range: lspRange{
						Start: lspPosition{Line: 2, Character: 0},
						End:   lspPosition{Line: 5, Character: 0},
					},
				},
			},
		},
	}

	flat := flattenDocumentSymbols(symbols)
	if len(flat) != 2 {
		t.Errorf("expected 2 flat symbols, got %d", len(flat))
	}

	if flat[0].Name != "Parent" || flat[0].Kind != "Class" || flat[0].StartLine != 1 {
		t.Errorf("unexpected parent symbol: %+v", flat[0])
	}

	if flat[1].Name != "Child" || flat[1].Kind != "Method" || flat[1].StartLine != 3 {
		t.Errorf("unexpected child symbol: %+v", flat[1])
	}
}

func TestLSP_GotoDefinitionHandler(t *testing.T) {
	tempCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	absCwd, err := filepath.Abs(tempCwd)
	if err != nil {
		t.Fatal(err)
	}

	client, server := setupMockLSPClient(t, absCwd)
	defer client.Close()
	defer server.writer.Close()

	dummyFile := filepath.Join(absCwd, "dummy.go")
	dummyContent := "package agent\n\nfunc Dummy() {\n\t// declaration line\n\tprintln(\"hello\")\n}\n"
	err = os.WriteFile(dummyFile, []byte(dummyContent), 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(dummyFile)

	stdCtx := context.WithValue(context.Background(), WorkdirKey, absCwd)
	ctx := &mockToolContext{Context: stdCtx}

	args := LSPGotoDefinitionArgs{
		Path:      "dummy.go",
		Line:      3,
		Character: 6,
	}

	result, err := LSPGotoDefinitionHandler(ctx, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Success {
		t.Errorf("expected definition lookup success, got: %s", result.Message)
	}

	if result.File != "dummy.go" {
		t.Errorf("expected result file 'dummy.go', got: %q", result.File)
	}

	if result.Line != 5 {
		t.Errorf("expected definition line 5, got %d", result.Line)
	}

	if !strings.Contains(result.Snippet, `println("hello")`) {
		t.Errorf("expected snippet to contain definition line, got:\n%s", result.Snippet)
	}
}

func TestLSP_FindReferencesHandler(t *testing.T) {
	tempCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	absCwd, err := filepath.Abs(tempCwd)
	if err != nil {
		t.Fatal(err)
	}

	client, server := setupMockLSPClient(t, absCwd)
	defer client.Close()
	defer server.writer.Close()

	dummyFile := filepath.Join(absCwd, "dummy.go")
	dummyContent := "package agent\n\nfunc Dummy() {\n\t// declaration line\n\tprintln(\"hello\")\n}\n"
	err = os.WriteFile(dummyFile, []byte(dummyContent), 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(dummyFile)

	stdCtx := context.WithValue(context.Background(), WorkdirKey, absCwd)
	ctx := &mockToolContext{Context: stdCtx}

	args := LSPFindReferencesArgs{
		Path:      "dummy.go",
		Line:      3,
		Character: 6,
	}

	result, err := LSPFindReferencesHandler(ctx, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Success {
		t.Errorf("expected references lookup success")
	}

	if result.Count != 1 {
		t.Errorf("expected 1 reference, got %d", result.Count)
	}

	if len(result.References) != 1 || result.References[0].File != "dummy.go" {
		t.Errorf("unexpected reference list: %+v", result.References)
	}

	if !strings.Contains(result.References[0].LineContent, `println("hello")`) {
		t.Errorf("expected line content to contain 'println(\"hello\")', got %q", result.References[0].LineContent)
	}
}

func TestLSP_DocumentSymbolsHandler(t *testing.T) {
	tempCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	absCwd, err := filepath.Abs(tempCwd)
	if err != nil {
		t.Fatal(err)
	}

	client, server := setupMockLSPClient(t, absCwd)
	defer client.Close()
	defer server.writer.Close()

	stdCtx := context.WithValue(context.Background(), WorkdirKey, absCwd)
	ctx := &mockToolContext{Context: stdCtx}

	args := LSPDocumentSymbolsArgs{
		Path: "dummy.go",
	}

	result, err := LSPDocumentSymbolsHandler(ctx, args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !result.Success {
		t.Errorf("expected document symbols success")
	}

	if result.Count != 2 {
		t.Errorf("expected 2 symbols, got %d", result.Count)
	}

	if result.Symbols[0].Name != "MyStruct" || result.Symbols[0].Kind != "Struct" {
		t.Errorf("unexpected first symbol: %+v", result.Symbols[0])
	}

	if result.Symbols[1].Name != "MyField" || result.Symbols[1].Kind != "Field" {
		t.Errorf("unexpected second symbol: %+v", result.Symbols[1])
	}
}

func TestLSP_SandboxViolation(t *testing.T) {
	tempCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	absCwd, err := filepath.Abs(tempCwd)
	if err != nil {
		t.Fatal(err)
	}

	stdCtx := context.WithValue(context.Background(), WorkdirKey, absCwd)
	ctx := &mockToolContext{Context: stdCtx}

	args := LSPGotoDefinitionArgs{
		Path:      "../escaped/file.go",
		Line:      1,
		Character: 1,
	}

	_, err = LSPGotoDefinitionHandler(ctx, args)
	if err == nil {
		t.Fatal("expected sandbox violation error, got nil")
	}

	if !strings.Contains(err.Error(), "security sandbox blocked") {
		t.Errorf("expected sandbox error, got: %v", err)
	}
}
