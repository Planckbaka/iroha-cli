package agent

import (
	"strings"
	"testing"
)

func TestTokenizeCommand_Simple(t *testing.T) {
	tokens, err := tokenizeCommand("ls -la /tmp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 3 {
		t.Fatalf("expected 3 tokens, got %d: %v", len(tokens), tokens)
	}
	if tokens[0] != "ls" || tokens[1] != "-la" || tokens[2] != "/tmp" {
		t.Errorf("unexpected tokens: %v", tokens)
	}
}

func TestTokenizeCommand_DoubleQuotes(t *testing.T) {
	tokens, err := tokenizeCommand(`echo "hello world" foo`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 3 {
		t.Fatalf("expected 3 tokens, got %d: %v", len(tokens), tokens)
	}
	if tokens[0] != "echo" || tokens[1] != "hello world" || tokens[2] != "foo" {
		t.Errorf("unexpected tokens: %v", tokens)
	}
}

func TestTokenizeCommand_SingleQuotes(t *testing.T) {
	tokens, err := tokenizeCommand(`echo 'hello world' foo`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tokens[1] != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", tokens[1])
	}
}

func TestTokenizeCommand_BackslashEscape(t *testing.T) {
	tokens, err := tokenizeCommand(`echo hello\ world`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d: %v", len(tokens), tokens)
	}
	if tokens[1] != "hello world" {
		t.Errorf("expected 'hello world', got '%s'", tokens[1])
	}
}

func TestTokenizeCommand_BackslashInDoubleQuotes(t *testing.T) {
	tokens, err := tokenizeCommand(`echo "hello \"world\""`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tokens[1] != `hello "world"` {
		t.Errorf("expected 'hello \"world\"', got '%s'", tokens[1])
	}
}

func TestTokenizeCommand_Empty(t *testing.T) {
	tokens, err := tokenizeCommand("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 0 {
		t.Errorf("expected 0 tokens, got %d", len(tokens))
	}
}

func TestTokenizeCommand_Whitespace(t *testing.T) {
	tokens, err := tokenizeCommand("   \t  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 0 {
		t.Errorf("expected 0 tokens, got %d", len(tokens))
	}
}

func TestTokenizeCommand_Pipe(t *testing.T) {
	_, err := tokenizeCommand("cat file.txt | grep foo")
	if err == nil {
		t.Fatal("expected error for pipe")
	}
	if !strings.Contains(err.Error(), "pipe") {
		t.Errorf("expected pipe error, got: %v", err)
	}
}

func TestTokenizeCommand_AndChain(t *testing.T) {
	_, err := tokenizeCommand("make && make test")
	if err == nil {
		t.Fatal("expected error for && chaining")
	}
	if !strings.Contains(err.Error(), "chaining") {
		t.Errorf("expected chaining error, got: %v", err)
	}
}

func TestTokenizeCommand_Semicolon(t *testing.T) {
	_, err := tokenizeCommand("echo foo; echo bar")
	if err == nil {
		t.Fatal("expected error for semicolon")
	}
	if !strings.Contains(err.Error(), "separator") {
		t.Errorf("expected separator error, got: %v", err)
	}
}

func TestTokenizeCommand_SubshellDollar(t *testing.T) {
	_, err := tokenizeCommand("echo $(whoami)")
	if err == nil {
		t.Fatal("expected error for $() subshell")
	}
	if !strings.Contains(err.Error(), "subshell") {
		t.Errorf("expected subshell error, got: %v", err)
	}
}

func TestTokenizeCommand_BacktickSubshell(t *testing.T) {
	_, err := tokenizeCommand("echo `whoami`")
	if err == nil {
		t.Fatal("expected error for backtick subshell")
	}
	if !strings.Contains(err.Error(), "subshell") {
		t.Errorf("expected subshell error, got: %v", err)
	}
}

func TestTokenizeCommand_OutputRedirect(t *testing.T) {
	_, err := tokenizeCommand("echo foo > out.txt")
	if err == nil {
		t.Fatal("expected error for > redirect")
	}
	if !strings.Contains(err.Error(), "redirection") {
		t.Errorf("expected redirection error, got: %v", err)
	}
}

func TestTokenizeCommand_AppendRedirect(t *testing.T) {
	_, err := tokenizeCommand("echo foo >> out.txt")
	if err == nil {
		t.Fatal("expected error for >> redirect")
	}
	if !strings.Contains(err.Error(), "redirection") {
		t.Errorf("expected redirection error, got: %v", err)
	}
}

func TestTokenizeCommand_InputRedirect(t *testing.T) {
	_, err := tokenizeCommand("sort < input.txt")
	if err == nil {
		t.Fatal("expected error for < redirect")
	}
	if !strings.Contains(err.Error(), "redirection") {
		t.Errorf("expected redirection error, got: %v", err)
	}
}

func TestTokenizeCommand_SingleAmpersandAllowed(t *testing.T) {
	// A single & should not trigger the chaining error (only && does)
	tokens, err := tokenizeCommand("echo foo &")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// & as its own token is fine — the tokenizer just sees it as a regular char
	// since only && is blocked
	if len(tokens) != 3 {
		t.Fatalf("expected 3 tokens, got %d: %v", len(tokens), tokens)
	}
}

func TestTokenizeCommand_ComplexQuoted(t *testing.T) {
	tokens, err := tokenizeCommand(`grep -rn "foo(bar)" --include='*.go' /src`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 5 {
		t.Fatalf("expected 5 tokens, got %d: %v", len(tokens), tokens)
	}
	if tokens[2] != "foo(bar)" {
		t.Errorf("expected 'foo(bar)', got '%s'", tokens[2])
	}
	if tokens[3] != "--include=*.go" {
		t.Errorf("expected '--include=*.go', got '%s'", tokens[3])
	}
}

func TestTokenizeCommand_QuotedPathWithSpaces(t *testing.T) {
	tokens, err := tokenizeCommand(`cat "/path/to/my file.txt"`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tokens[1] != "/path/to/my file.txt" {
		t.Errorf("expected '/path/to/my file.txt', got '%s'", tokens[1])
	}
}

func TestTokenizeCommand_OrChain(t *testing.T) {
	// Note: || contains a pipe character which is caught first
	_, err := tokenizeCommand("make || echo failed")
	if err == nil {
		t.Fatal("expected error for ||")
	}
}

func TestTokenizeCommand_DollarWithoutParen(t *testing.T) {
	// $ without () should be tokenized normally
	tokens, err := tokenizeCommand("echo $HOME")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tokens[1] != "$HOME" {
		t.Errorf("expected '$HOME', got '%s'", tokens[1])
	}
}

func TestTokenizeCommand_TrailingBackslash(t *testing.T) {
	tokens, err := tokenizeCommand(`echo foo\`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tokens) != 2 {
		t.Fatalf("expected 2 tokens, got %d: %v", len(tokens), tokens)
	}
	// Trailing backslash with no char to escape is kept as literal 'foo\'
	if tokens[1] != "foo\\" {
		t.Errorf("expected 'foo\\', got '%s'", tokens[1])
	}
}

func TestTokenizeCommand_AdjacentQuotedAndUnquoted(t *testing.T) {
	// Two adjacent tokens, one quoted, one not
	tokens, err := tokenizeCommand(`echo "hello"world`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tokens[1] != "helloworld" {
		t.Errorf("expected 'helloworld', got '%s'", tokens[1])
	}
}

func TestSafePrefixes_Default(t *testing.T) {
	if len(safePrefixes) == 0 {
		t.Error("expected default safePrefixes to be populated")
	}
	found := false
	for _, p := range safePrefixes {
		if p == "/usr" {
			found = true
		}
	}
	if !found {
		t.Error("expected /usr in default safePrefixes")
	}
}
