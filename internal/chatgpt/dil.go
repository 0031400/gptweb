package chatgpt

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var dilRootTags = map[string]bool{
	"box": true, "row": true, "grid": true, "card": true,
	"flow": true, "carousel": true, "form": true, "col": true,
}

var dilInteractivePattern = regexp.MustCompile(`(?is)<(?:button|input|textarea|slider|select|radio|radio-group|checkbox|date-picker|segmented-control|popover)\b`)

// deriveDILBlocks 在 parts 里找出需要 DIL 运行时才能渲染的整块区域。
// 只做块级识别，不解析/执行 DIL；前端把这些区间统一显示为「无法实现」。
func deriveDILBlocks(text string) []GenUIComponent {
	lines := textLines(text)
	starts := make([]int, 0)
	inFence := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line.text)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		if strings.HasPrefix(trimmed, "{@body") {
			starts = append(starts, line.start)
			continue
		}
		tagStart := line.start + len(line.text) - len(strings.TrimLeft(line.text, " \t"))
		if strings.HasPrefix(text[tagStart:], "<") && !strings.HasPrefix(text[tagStart:], "</") {
			if dilRootTags[tagNameAt(text, tagStart)] {
				starts = append(starts, line.start)
			}
		}
	}

	var blocks []GenUIComponent
	coveredUntil := 0
	for _, start := range starts {
		if start < coveredUntil {
			continue
		}
		rootStart := start
		bodyBlock := strings.HasPrefix(strings.TrimSpace(text[start:]), "{@body")
		if bodyBlock {
			cursor := nextLineStart(text, start)
			for cursor < len(text) {
				lineEnd := lineEndAt(text, cursor)
				line := strings.TrimSpace(text[cursor:lineEnd])
				if line == "" || strings.HasPrefix(line, "{@body") {
					cursor = lineEnd + 1
					continue
				}
				break
			}
			relative := strings.IndexByte(text[cursor:], '<')
			if relative < 0 {
				continue
			}
			rootStart = cursor + relative
			if strings.TrimSpace(text[cursor:rootStart]) != "" {
				continue
			}
		}
		name := tagNameAt(text, rootStart)
		if !dilRootTags[name] {
			continue
		}
		end := matchingTagEnd(text, rootStart, name)
		if end <= start {
			continue
		}
		if !bodyBlock && !dilInteractivePattern.MatchString(text[rootStart:end]) {
			continue
		}
		blocks = append(blocks, GenUIComponent{
			Type:       "dil_block",
			StartIndex: utf8.RuneCountInString(text[:start]),
			EndIndex:   utf8.RuneCountInString(text[:end]),
		})
		coveredUntil = end
	}
	return blocks
}

type textLine struct {
	start int
	text  string
}

func textLines(text string) []textLine {
	lines := make([]textLine, 0)
	for start := 0; start <= len(text); {
		end := lineEndAt(text, start)
		lines = append(lines, textLine{start: start, text: text[start:end]})
		if end >= len(text) {
			break
		}
		start = end + 1
	}
	return lines
}

func lineEndAt(text string, start int) int {
	if start >= len(text) {
		return len(text)
	}
	if relative := strings.IndexByte(text[start:], '\n'); relative >= 0 {
		return start + relative
	}
	return len(text)
}

func nextLineStart(text string, start int) int {
	end := lineEndAt(text, start)
	if end >= len(text) {
		return len(text)
	}
	return end + 1
}

func tagNameAt(text string, position int) string {
	if position >= len(text) || text[position] != '<' {
		return ""
	}
	cursor := position + 1
	if cursor < len(text) && text[cursor] == '/' {
		cursor++
	}
	start := cursor
	for cursor < len(text) {
		char := text[cursor]
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' {
			cursor++
			continue
		}
		break
	}
	return text[start:cursor]
}

func matchingTagEnd(text string, open int, name string) int {
	openEnd := tagEnd(text, open)
	if openEnd < 0 {
		return -1
	}
	if openEnd >= 2 && text[openEnd-2] == '/' {
		return openEnd
	}
	depth := 1
	cursor := openEnd
	for cursor < len(text) {
		relative := strings.IndexByte(text[cursor:], '<')
		if relative < 0 {
			return -1
		}
		position := cursor + relative
		next := tagEnd(text, position)
		if next < 0 {
			return -1
		}
		if tagNameAt(text, position) == name {
			closing := position+1 < len(text) && text[position+1] == '/'
			selfClosing := next >= 2 && text[next-2] == '/'
			if closing {
				depth--
				if depth == 0 {
					return next
				}
			} else if !selfClosing {
				depth++
			}
		}
		cursor = next
	}
	return -1
}

func tagEnd(text string, position int) int {
	quote := byte(0)
	braces := 0
	for cursor := position + 1; cursor < len(text); cursor++ {
		char := text[cursor]
		if quote != 0 {
			if char == quote {
				quote = 0
			}
			continue
		}
		switch char {
		case '"', '\'', '`':
			quote = char
		case '{':
			braces++
		case '}':
			if braces > 0 {
				braces--
			}
		case '>':
			if braces == 0 {
				return cursor + 1
			}
		}
	}
	return -1
}

// mergeDILBlocks 让整块 DIL 覆盖其中的顶层 GenUI，避免 UI 只替换局部内容。
func mergeDILBlocks(components, blocks []GenUIComponent) []GenUIComponent {
	if len(blocks) == 0 {
		return components
	}
	merged := make([]GenUIComponent, 0, len(components)+len(blocks))
	for _, component := range components {
		inside := false
		for _, block := range blocks {
			if component.StartIndex >= block.StartIndex && component.EndIndex <= block.EndIndex {
				inside = true
				break
			}
		}
		if !inside {
			merged = append(merged, component)
		}
	}
	merged = append(merged, blocks...)
	return merged
}
