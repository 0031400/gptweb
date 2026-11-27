package chatgpt

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"
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
	components = mergeDILBlocks(components, deriveDILBlocks(text))
	if len(components) == 0 {
		return nil
	}
	sort.SliceStable(components, func(i, j int) bool {
		return components[i].StartIndex < components[j].StartIndex
	})
	return components
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
