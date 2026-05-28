package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// tokenizeKeywords splits text into lowercase words, skipping short common stop words
func tokenizeKeywords(text string) []string {
	text = strings.ToLower(text)
	// Replace non-alphanumeric with spaces
	re := regexp.MustCompile(`[^a-z0-9]+`)
	cleaned := re.ReplaceAllString(text, " ")
	words := strings.Fields(cleaned)

	stopWords := map[string]bool{
		"the": true, "and": true, "for": true, "you": true, "with": true, "this": true, "that": true,
		"your": true, "from": true, "have": true, "will": true, "about": true, "want": true,
	}

	var res []string
	for _, w := range words {
		if len(w) >= 3 && !stopWords[w] {
			res = append(res, w)
		}
	}
	return res
}

// projectMemoryDir returns ./.iroha/memory (creating if needed).
func projectMemoryDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Join(cwd, ".iroha", "memory"), nil
}
