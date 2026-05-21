package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComputeFileDiff_NewFile(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "new_file.txt")

	newContent := "line 1\nline 2\nline 3"
	diff := computeFileDiff(filePath, newContent)

	expectedHeader := "@@ -0,0 +1,3 @@"
	if !strings.Contains(diff, expectedHeader) {
		t.Errorf("Expected diff header %q, got:\n%s", expectedHeader, diff)
	}

	expectedLines := []string{
		"+ line 1",
		"+ line 2",
		"+ line 3",
	}
	for _, expected := range expectedLines {
		if !strings.Contains(diff, expected) {
			t.Errorf("Expected diff to contain addition %q, got:\n%s", expected, diff)
		}
	}
}

func TestComputeFileDiff_Unchanged(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "unchanged.txt")

	content := "hello world\nfoo bar\nbaz"
	err := os.WriteFile(filePath, []byte(content), 0644)
	if err != nil {
		t.Fatal(err)
	}

	diff := computeFileDiff(filePath, content)
	if diff != "" {
		t.Errorf("Expected empty diff for unchanged content, got:\n%s", diff)
	}
}

func TestComputeFileDiff_Modified(t *testing.T) {
	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "modified.txt")

	oldContent := "line 1\nline 2\nline 3\nline 4\nline 5\nline 6\nline 7"
	err := os.WriteFile(filePath, []byte(oldContent), 0644)
	if err != nil {
		t.Fatal(err)
	}

	// Change line 4 (replace it) and add a line at the end
	newContent := "line 1\nline 2\nline 3\nline four modified\nline 5\nline 6\nline 7\nline 8 new"
	diff := computeFileDiff(filePath, newContent)

	// Verify header and changes exist
	if !strings.Contains(diff, "- line 4") {
		t.Errorf("Expected deletion of line 4, diff was:\n%s", diff)
	}
	if !strings.Contains(diff, "+ line four modified") {
		t.Errorf("Expected modification of line 4, diff was:\n%s", diff)
	}
	if !strings.Contains(diff, "+ line 8 new") {
		t.Errorf("Expected addition of line 8, diff was:\n%s", diff)
	}

	// Verify that unchanged lines in between (e.g. line 2) are present as context
	if !strings.Contains(diff, "  line 2") {
		t.Errorf("Expected line 2 to be present as context, diff was:\n%s", diff)
	}
}
