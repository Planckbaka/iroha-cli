package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// agentsMDMu protects concurrent reads/writes to AGENTS.md.
var agentsMDMu sync.Mutex

type agentsBlock struct {
	name       string
	headerLine string
	bodyLines  []string
}

func syncToAgentsMD(entry *MemoryEntry, isDelete bool) error {
	agentsMDMu.Lock()
	defer agentsMDMu.Unlock()

	agentsPath := "AGENTS.md"

	// Read AGENTS.md
	data, err := os.ReadFile(agentsPath)
	var content string
	if err != nil {
		if os.IsNotExist(err) {
			content = "# Project Agents Configuration\n\n"
		} else {
			return err
		}
	} else {
		content = string(data)
	}

	const sectionTitle = "## Agent Dynamic Learnings"

	// Find or create the ## Agent Dynamic Learnings section
	var beforeSection, afterSection string
	idx := strings.Index(content, sectionTitle)
	if idx == -1 {
		if !strings.HasSuffix(content, "\n\n") {
			if strings.HasSuffix(content, "\n") {
				content += "\n"
			} else {
				content += "\n\n"
			}
		}
		content += sectionTitle + "\n\n"
		beforeSection = content
		afterSection = ""
	} else {
		beforeSection = content[:idx+len(sectionTitle)]
		afterSection = content[idx+len(sectionTitle):]
	}

	// Parse blocks from afterSection
	lines := strings.Split(afterSection, "\n")
	var newSectionLines []string
	var blocks []agentsBlock
	var currentBlock *agentsBlock

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- **") && strings.Contains(trimmed, "** (") {
			if currentBlock != nil {
				blocks = append(blocks, *currentBlock)
			}
			nameEnd := strings.Index(trimmed[4:], "**")
			var name string
			if nameEnd != -1 {
				name = trimmed[4 : 4+nameEnd]
			}
			currentBlock = &agentsBlock{
				name:       name,
				headerLine: line,
			}
		} else if currentBlock != nil && (strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t") || line == "") {
			currentBlock.bodyLines = append(currentBlock.bodyLines, line)
		} else {
			if currentBlock != nil {
				blocks = append(blocks, *currentBlock)
				currentBlock = nil
			}
			newSectionLines = append(newSectionLines, line)
		}
	}
	if currentBlock != nil {
		blocks = append(blocks, *currentBlock)
	}

	// Update or delete blocks
	found := false
	var updatedBlocks []agentsBlock
	for _, b := range blocks {
		if b.name == entry.Name {
			found = true
			if !isDelete {
				updatedBlocks = append(updatedBlocks, makeAgentsBlock(entry))
			}
		} else {
			updatedBlocks = append(updatedBlocks, b)
		}
	}
	if !found && !isDelete {
		updatedBlocks = append(updatedBlocks, makeAgentsBlock(entry))
	}

	// Reconstruct the section
	var sb strings.Builder
	for _, b := range updatedBlocks {
		sb.WriteString(b.headerLine + "\n")
		for _, bl := range b.bodyLines {
			sb.WriteString(bl + "\n")
		}
	}

	nonEmptyFound := false
	var trailingLines []string
	for i := len(newSectionLines) - 1; i >= 0; i-- {
		l := newSectionLines[i]
		if strings.TrimSpace(l) != "" {
			nonEmptyFound = true
		}
		if nonEmptyFound {
			trailingLines = append([]string{l}, trailingLines...)
		}
	}
	for _, tl := range trailingLines {
		sb.WriteString(tl + "\n")
	}

	finalContent := beforeSection + "\n" + sb.String()
	reConsecutiveNewlines := regexp.MustCompile(`\n{3,}`)
	finalContent = reConsecutiveNewlines.ReplaceAllString(finalContent, "\n\n")

	return os.WriteFile(agentsPath, []byte(finalContent), 0644)
}

func makeAgentsBlock(entry *MemoryEntry) agentsBlock {
	header := fmt.Sprintf("- **%s** (%s): %s", entry.Name, entry.Type, entry.Description)
	contentLines := strings.Split(entry.Content, "\n")
	var body []string
	if len(contentLines) > 0 && strings.TrimSpace(entry.Content) != "" {
		body = append(body, "  - *Content*:")
		for _, cl := range contentLines {
			body = append(body, "    "+cl)
		}
	}
	return agentsBlock{
		name:       entry.Name,
		headerLine: header,
		bodyLines:  body,
	}
}

func (mm *MemoryManager) syncFromAgentsMDLocked() error {
	agentsMDMu.Lock()
	defer agentsMDMu.Unlock()

	agentsPath := "AGENTS.md"
	data, err := os.ReadFile(agentsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	const sectionTitle = "## Agent Dynamic Learnings"
	content := string(data)
	idx := strings.Index(content, sectionTitle)
	if idx == -1 {
		return nil
	}

	afterSection := content[idx+len(sectionTitle):]
	lines := strings.Split(afterSection, "\n")

	type parsedBlock struct {
		name        string
		memType     MemoryType
		description string
		content     string
	}
	var parsedBlocks []parsedBlock
	var currentBlock *parsedBlock
	var contentLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- **") && strings.Contains(trimmed, "** (") {
			if currentBlock != nil {
				currentBlock.content = strings.TrimSpace(strings.Join(contentLines, "\n"))
				parsedBlocks = append(parsedBlocks, *currentBlock)
				contentLines = nil
			}

			nameEnd := strings.Index(trimmed[4:], "**")
			if nameEnd == -1 {
				currentBlock = nil
				continue
			}
			name := trimmed[4 : 4+nameEnd]

			rest := strings.TrimSpace(trimmed[4+nameEnd+2:])
			typeEnd := strings.Index(rest, ")")
			if typeEnd == -1 || !strings.HasPrefix(rest, "(") {
				currentBlock = nil
				continue
			}
			tStr := rest[1:typeEnd]

			desc := ""
			descIdx := strings.Index(rest[typeEnd:], ": ")
			if descIdx != -1 {
				desc = strings.TrimSpace(rest[typeEnd+descIdx+2:])
			}

			currentBlock = &parsedBlock{
				name:        name,
				memType:     MemoryType(tStr),
				description: desc,
			}
		} else if currentBlock != nil && (strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t") || line == "") {
			trimmedLine := strings.TrimSpace(line)
			if trimmedLine == "- *Content*:" || trimmedLine == "*Content*:" {
				continue
			}
			if strings.HasPrefix(line, "    ") {
				contentLines = append(contentLines, line[4:])
			} else if strings.HasPrefix(line, "  ") {
				contentLines = append(contentLines, line[2:])
			} else {
				contentLines = append(contentLines, trimmedLine)
			}
		} else {
			if currentBlock != nil {
				currentBlock.content = strings.TrimSpace(strings.Join(contentLines, "\n"))
				parsedBlocks = append(parsedBlocks, *currentBlock)
				currentBlock = nil
				contentLines = nil
			}
		}
	}
	if currentBlock != nil {
		currentBlock.content = strings.TrimSpace(strings.Join(contentLines, "\n"))
		parsedBlocks = append(parsedBlocks, *currentBlock)
	}

	saveDir, err := projectMemoryDir()
	if err != nil {
		return err
	}

	presentNames := make(map[string]bool)

	for _, pb := range parsedBlocks {
		if !validMemoryTypes[pb.memType] {
			continue
		}
		presentNames[pb.name] = true

		existing, exists := mm.entries[pb.name]

		if !exists || existing.Description != pb.description || existing.Content != pb.content || existing.Type != pb.memType {
			filename := slugify(pb.name) + ".md"
			now := time.Now().UTC()
			entry := &MemoryEntry{
				Name:        pb.name,
				Description: pb.description,
				Type:        pb.memType,
				Content:     pb.content,
				UpdatedAt:   now,
				File:        filename,
			}

			_ = os.MkdirAll(saveDir, 0755)
			filePath := filepath.Join(saveDir, filename)
			text := renderFrontmatter(entry)
			_ = os.WriteFile(filePath, []byte(text), 0600)

			mm.entries[pb.name] = entry
			if len(mm.dirs) == 0 || mm.dirs[len(mm.dirs)-1] != saveDir {
				mm.dirs = append(mm.dirs, saveDir)
			}
		}
	}

	var toDelete []string
	for name, entry := range mm.entries {
		filePath := filepath.Join(saveDir, entry.File)
		if _, err := os.Stat(filePath); err == nil {
			if !presentNames[name] {
				toDelete = append(toDelete, name)
			}
		}
	}

	for _, name := range toDelete {
		_ = mm.deleteLocked(name, true)
	}

	if len(parsedBlocks) > 0 || len(toDelete) > 0 {
		mm.rebuildIndexLocked(saveDir)
	}

	return nil
}
