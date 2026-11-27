package chatgpt

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

var tagAttributePattern = regexp.MustCompile(`([A-Za-z_][\w-]*)\s*=\s*"([^"]*)"`)

// normalizeGenUI 把上游 genui_components 整理成前端需要的最小数据。
// start_index/end_index 原样透传，前端直接用 text.slice 删除区间并插入组件。
func normalizeGenUI(text string, metadata map[string]any) []GenUIComponent {
	raw, _ := metadata["genui_components"].([]any)
	results := componentResults(metadata)
	components := make([]GenUIComponent, 0, len(raw))
	for _, item := range raw {
		value, ok := item.(map[string]any)
		if !ok {
			continue
		}
		componentType, _ := value["type"].(string)
		if componentType == "" {
			continue
		}
		start := intValue(value["start_index"])
		end := intValue(value["end_index"])
		source := genuiSource(text, start, end)
		components = append(components, GenUIComponent{
			Type:       componentType,
			StartIndex: start,
			EndIndex:   end,
			Data:       genuiData(componentType, source, value, results),
		})
	}
	// {@body ... DIL.useState(...)} 交互块没有 genui_components 条目，
	// 按 parts 里的声明 + 后续顶层容器推导一个区间，前端统一显示「未实现」。
	components = append(components, deriveInteractiveBlocks(text, components)...)
	if len(components) == 0 {
		return nil
	}
	sort.SliceStable(components, func(i, j int) bool {
		return components[i].StartIndex < components[j].StartIndex
	})
	return components
}

func deriveInteractiveBlocks(text string, existing []GenUIComponent) []GenUIComponent {
	var components []GenUIComponent
	searchAt := 0
	for {
		relative := strings.Index(text[searchAt:], "{@body")
		if relative < 0 {
			break
		}
		start := searchAt + relative
		searchAt = start + len("{@body")
		lineStart := strings.LastIndexByte(text[:start], '\n') + 1
		if strings.TrimSpace(text[lineStart:start]) != "" || insideCodeFence(text, start) {
			continue
		}
		cursor := lineStart
		for cursor < len(text) {
			lineEnd := strings.IndexByte(text[cursor:], '\n')
			if lineEnd < 0 {
				lineEnd = len(text)
			} else {
				lineEnd += cursor
			}
			line := strings.TrimSpace(text[cursor:lineEnd])
			if line == "" || strings.HasPrefix(line, "{@body") {
				cursor = lineEnd + 1
				continue
			}
			break
		}
		relative = strings.IndexByte(text[cursor:], '<')
		if relative < 0 {
			continue
		}
		open := cursor + relative
		if strings.TrimSpace(text[cursor:open]) != "" {
			continue
		}
		name := tagNameAt(text, open)
		if name == "" {
			continue
		}
		end := matchingTagEnd(text, open, name)
		if end <= start {
			continue
		}
		startIndex := utf8.RuneCountInString(text[:lineStart])
		endIndex := utf8.RuneCountInString(text[:end])
		if overlapsGenUI(existing, startIndex, endIndex) || overlapsGenUI(components, startIndex, endIndex) {
			searchAt = end
			continue
		}
		components = append(components, GenUIComponent{
			Type:       "interactive_block",
			StartIndex: startIndex,
			EndIndex:   endIndex,
		})
		searchAt = end
	}
	return components
}

func insideCodeFence(text string, position int) bool {
	inFence := false
	for _, line := range strings.Split(text[:position], "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
		}
	}
	return inFence
}

func overlapsGenUI(components []GenUIComponent, start, end int) bool {
	for _, component := range components {
		if start < component.EndIndex && component.StartIndex < end {
			return true
		}
	}
	return false
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
		case '"', '\'':
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

func genuiSource(text string, start, end int) string {
	runes := []rune(text)
	if start >= 0 && end > start && end <= len(runes) {
		source := string(runes[start:end])
		if strings.HasPrefix(strings.TrimSpace(source), "<") {
			return source
		}
	}
	units := utf16.Encode(runes)
	if start >= 0 && end > start && end <= len(units) {
		source := string(utf16.Decode(units[start:end]))
		if strings.HasPrefix(strings.TrimSpace(source), "<") {
			return source
		}
	}
	return ""
}

func genuiData(componentType, source string, value map[string]any, results map[string]any) map[string]any {
	switch componentType {
	case "code_block":
		attrs, body, ok := parseWrappedTag(source, "CodeBlock")
		if !ok {
			return nil
		}
		return map[string]any{"language": attrs["language"], "code": body}
	case "writing_block":
		attrs, body, ok := parseWrappedTag(source, "WritingBlock")
		if !ok {
			return nil
		}
		return map[string]any{
			"subject": attrs["subject"],
			"variant": attrs["variant"],
			"body":    body,
		}
	case "async_image":
		attrs := tagAttributes(source)
		data := map[string]any{}
		for _, key := range []string{"query", "aspectRatio", "width", "maxWidth"} {
			if attrs[key] != "" {
				data[key] = attrs[key]
			}
		}
		if id, _ := value["component_resolution_id"].(string); id != "" {
			if state := resolvedState(results, id); state != nil {
				if images := compactImages(state["images"]); len(images) > 0 {
					data["images"] = images
				}
			}
		}
		if len(data) == 0 {
			return nil
		}
		return data
	case "link":
		attrs := tagAttributes(source)
		data := map[string]any{}
		if id, _ := value["component_resolution_id"].(string); id != "" {
			if state := resolvedState(results, id); state != nil {
				if text := stringValue(state["url"]); text != "" {
					data["url"] = text
				}
				if text := stringValue(state["title"]); text != "" {
					data["title"] = text
				}
			}
		}
		if _, exists := data["url"]; !exists && attrs["url"] != "" {
			data["url"] = attrs["url"]
		}
		if _, exists := data["title"]; !exists && attrs["title"] != "" {
			data["title"] = attrs["title"]
		}
		if len(data) == 0 {
			return nil
		}
		return data
	case "charts_widget_v2":
		return chartData(source)
	default:
		// map 等未实现类型不需要携带数据；前端按 type 显示「未实现」。
		return nil
	}
}

func tagAttributes(source string) map[string]string {
	head := source
	if end := strings.IndexByte(source, '>'); end >= 0 {
		head = source[:end]
	}
	result := map[string]string{}
	for _, match := range tagAttributePattern.FindAllStringSubmatch(head, -1) {
		if len(match) >= 3 {
			result[match[1]] = match[2]
		}
	}
	return result
}

func parseWrappedTag(source, tag string) (map[string]string, string, bool) {
	trimmed := strings.TrimSpace(source)
	if !strings.HasPrefix(trimmed, "<"+tag) {
		return nil, "", false
	}
	openEnd := strings.IndexByte(trimmed, '>')
	closeStart := strings.LastIndex(trimmed, "</"+tag+">")
	if openEnd < 0 || closeStart < openEnd {
		return nil, "", false
	}
	body := trimmed[openEnd+1 : closeStart]
	body = strings.TrimPrefix(body, "\n")
	body = strings.TrimRight(body, "\n \t")
	return tagAttributes(trimmed[:openEnd+1]), body, true
}

func componentResults(metadata map[string]any) map[string]any {
	dil, _ := metadata["model_dil_v2"].(map[string]any)
	appData, _ := dil["appData"].(map[string]any)
	opGenui, _ := appData["opGenui"].(map[string]any)
	results, _ := opGenui["componentResults"].(map[string]any)
	return results
}

func resolvedState(results map[string]any, id string) map[string]any {
	result, _ := results[id].(map[string]any)
	state, _ := result["state"].(map[string]any)
	return state
}

func compactImages(value any) []any {
	list, _ := value.([]any)
	images := make([]any, 0, len(list))
	for _, item := range list {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		image := map[string]any{}
		for _, key := range []string{"thumbnail_url", "content_url", "url", "title"} {
			if text := stringValue(entry[key]); text != "" {
				image[key] = text
			}
		}
		if len(image) > 0 {
			images = append(images, image)
		}
	}
	return images
}

func chartData(source string) map[string]any {
	open := strings.Index(source, "{{")
	close := strings.LastIndex(source, "}}")
	if open < 0 || close <= open {
		return nil
	}
	var content map[string]any
	if json.Unmarshal([]byte("{"+source[open+2:close]+"}"), &content) != nil {
		return nil
	}
	data, _ := content["data"].([]any)
	if len(data) == 0 {
		return nil
	}
	result := map[string]any{
		"chartType": stringValue(content["chartType"]),
		"data":      data,
	}
	for _, key := range []string{"xKey", "nameKey", "valueKey"} {
		if text := stringValue(content[key]); text != "" {
			result[key] = text
		}
	}
	if series, ok := content["series"].([]any); ok && len(series) > 0 {
		compact := make([]any, 0, len(series))
		for _, raw := range series {
			entry, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			item := map[string]any{}
			for _, key := range []string{"dataKey", "label", "valuePrefix", "valueSuffix"} {
				if text := stringValue(entry[key]); text != "" {
					item[key] = text
				}
			}
			if len(item) > 0 {
				compact = append(compact, item)
			}
		}
		if len(compact) > 0 {
			result["series"] = compact
		}
	}
	if meta, ok := content["meta"].(map[string]any); ok {
		if description := stringValue(meta["description"]); description != "" {
			result["description"] = description
		}
	}
	return result
}
