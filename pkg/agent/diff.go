package agent

import (
	"fmt"
	"os"
	"strings"
)

type diffOp struct {
	typ     rune   // ' ' (common), '+' (addition), '-' (deletion)
	line    string
	origIdx int    // 1-based line number in original file
	newIdx  int    // 1-based line number in new file
}

// computeFileDiff reads the file at path and generates a beautiful colored unified diff
// comparing its current content with newContent.
func computeFileDiff(path string, newContent string) string {
	var oldLines []string
	if data, err := os.ReadFile(path); err == nil {
		oldLines = strings.Split(string(data), "\n")
		// Trim optional trailing empty line from split
		if len(oldLines) > 0 && oldLines[len(oldLines)-1] == "" && len(string(data)) > 0 {
			oldLines = oldLines[:len(oldLines)-1]
		}
	} else {
		// New file — treat as empty
		oldLines = []string{}
	}

	newLines := strings.Split(newContent, "\n")
	if len(newLines) > 0 && newLines[len(newLines)-1] == "" && len(newContent) > 0 {
		newLines = newLines[:len(newLines)-1]
	}

	// 1. Calculate LCS (Longest Common Subsequence) DP Table
	m, n := len(oldLines), len(newLines)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}

	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if oldLines[i-1] == newLines[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = dp[i-1][j]
				if dp[i][j-1] > dp[i][j] {
					dp[i][j] = dp[i][j-1]
				}
			}
		}
	}

	// 2. Backtrack to find diff operations
	var ops []diffOp
	i, j := m, n
	for i > 0 || j > 0 {
		if i > 0 && j > 0 && oldLines[i-1] == newLines[j-1] {
			ops = append(ops, diffOp{typ: ' ', line: oldLines[i-1], origIdx: i, newIdx: j})
			i--
			j--
		} else if j > 0 && (i == 0 || dp[i][j-1] >= dp[i-1][j]) {
			ops = append(ops, diffOp{typ: '+', line: newLines[j-1], newIdx: j})
			j--
		} else if i > 0 && (j == 0 || dp[i][j-1] < dp[i-1][j]) {
			ops = append(ops, diffOp{typ: '-', line: oldLines[i-1], origIdx: i})
			i--
		}
	}

	// Reverse ops to chronological order
	for x, y := 0, len(ops)-1; x < y; x, y = x+1, y-1 {
		ops[x], ops[y] = ops[y], ops[x]
	}

	// 3. Mark lines to be shown based on 3 context lines around edits
	show := make([]bool, len(ops))
	const contextSize = 3

	for idx, op := range ops {
		if op.typ != ' ' {
			// Mark window around this edit
			start := idx - contextSize
			if start < 0 {
				start = 0
			}
			end := idx + contextSize
			if end >= len(ops) {
				end = len(ops) - 1
			}
			for k := start; k <= end; k++ {
				show[k] = true
			}
		}
	}

	// 4. Group marked lines into hunks and format output
	var sb strings.Builder
	inHunk := false
	var hunkOps []diffOp

	flushHunk := func() {
		if len(hunkOps) == 0 {
			return
		}
		// Calculate Hunk Header: @@ -origStart,origLen +newStart,newLen @@
		origStart, origLen := 0, 0
		newStart, newLen := 0, 0

		for _, hop := range hunkOps {
			if hop.typ != '+' {
				origLen++
				if origStart == 0 && hop.origIdx > 0 {
					origStart = hop.origIdx
				}
			}
			if hop.typ != '-' {
				newLen++
				if newStart == 0 && hop.newIdx > 0 {
					newStart = hop.newIdx
				}
			}
		}

		if origStart == 0 {
			origStart = 1
		}
		if newStart == 0 {
			newStart = 1
		}

		// Cyan hunk header
		sb.WriteString(fmt.Sprintf("\x1b[36m@@ -%d,%d +%d,%d @@\x1b[0m\n", origStart, origLen, newStart, newLen))

		for _, hop := range hunkOps {
			switch hop.typ {
			case ' ':
				sb.WriteString(fmt.Sprintf("  %s\n", hop.line))
			case '+':
				sb.WriteString(fmt.Sprintf("\x1b[32m+ %s\x1b[0m\n", hop.line))
			case '-':
				sb.WriteString(fmt.Sprintf("\x1b[31m- %s\x1b[0m\n", hop.line))
			}
		}
		hunkOps = nil
	}

	// If it is a completely new file, or previous file was empty, simply show everything as additions
	if len(oldLines) == 0 {
		sb.WriteString(fmt.Sprintf("\x1b[36m@@ -0,0 +1,%d @@\x1b[0m\n", len(newLines)))
		for _, line := range newLines {
			sb.WriteString(fmt.Sprintf("\x1b[32m+ %s\x1b[0m\n", line))
		}
		return sb.String()
	}

	for idx, op := range ops {
		if show[idx] {
			if !inHunk {
				inHunk = true
			}
			hunkOps = append(hunkOps, op)
		} else {
			if inHunk {
				flushHunk()
				inHunk = false
			}
		}
	}
	if inHunk {
		flushHunk()
	}

	return sb.String()
}
