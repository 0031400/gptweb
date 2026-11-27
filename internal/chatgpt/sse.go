package chatgpt

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ---------------- SSE ----------------

type partsSSEState struct {
	currentID string
	current   string
	bestID    string
	best      string
	emitted   string
	emittedID string
	metadata  map[string]any
	genuiHash string
	onGenUI   func([]GenUIComponent) error
}

func (s *partsSSEState) update(value string, onDelta, onReplace func(string) error) error {
	if value == "" {
		return nil
	}
	s.current = value
	s.best = value
	s.bestID = s.currentID
	if onDelta == nil && onReplace == nil {
		return nil
	}
	if s.emittedID == "" {
		s.emittedID = s.currentID
	}
	// 只对当前消息做增量输出；切换到新的助手消息时等 done 再整体替换。
	if s.emittedID != s.currentID {
		return nil
	}
	if value == s.emitted {
		return nil
	}
	if strings.HasPrefix(value, s.emitted) {
		next := value[len(s.emitted):]
		s.emitted = value
		if next != "" && onDelta != nil {
			return onDelta(next)
		}
		return nil
	}
	// content.parts 也可能用 replace 覆盖整段文本，无法用增量表达。
	s.emitted = value
	if onReplace != nil {
		return onReplace(value)
	}
	return nil
}

func (s *partsSSEState) setMessage(message map[string]any, onDelta, onReplace func(string) error) error {
	author, _ := message["author"].(map[string]any)
	if author["role"] != "assistant" {
		return nil
	}
	channel, _ := message["channel"].(string)
	if channel != "" && channel != "final" {
		return nil
	}
	id, _ := message["id"].(string)
	if id != "" && id != s.currentID {
		s.currentID = id
		s.current = ""
		s.metadata = nil
		s.genuiHash = ""
	}
	if metadata, ok := message["metadata"].(map[string]any); ok {
		s.metadata = metadata
	}
	content, _ := message["content"].(map[string]any)
	if value := contentText(content); value != "" {
		if err := s.update(value, onDelta, onReplace); err != nil {
			return err
		}
	}
	return s.emitGenUI()
}

func (s *partsSSEState) emitGenUI() error {
	if s.onGenUI == nil || s.currentID == "" {
		return nil
	}
	components := normalizeGenUI(s.current, s.metadata)
	if len(components) == 0 && s.genuiHash == "" {
		return nil
	}
	data, _ := json.Marshal(components)
	hash := string(data)
	if hash == s.genuiHash {
		return nil
	}
	s.genuiHash = hash
	return s.onGenUI(components)
}

func (s *partsSSEState) applyPatch(operation map[string]any, onDelta, onReplace func(string) error) error {
	path, _ := operation["p"].(string)
	if strings.HasPrefix(path, "/message/metadata") {
		return s.applyMetadataOperation(operation)
	}
	action, _ := operation["o"].(string)
	value, _ := operation["v"].(string)
	switch {
	case path == "/message/content/parts/0":
		switch action {
		case "append":
			return s.update(s.current+value, onDelta, onReplace)
		case "replace", "add":
			if value != "" {
				return s.update(value, onDelta, onReplace)
			}
			if action == "replace" {
				s.current = ""
			}
		case "remove":
			s.current = ""
		}
	case path == "/message/content/parts":
		if parts, ok := operation["v"].([]any); ok {
			s.current = ""
			if text := contentText(map[string]any{"parts": parts}); text != "" {
				return s.update(text, onDelta, onReplace)
			}
		}
	case path == "/message/content":
		if content, ok := operation["v"].(map[string]any); ok {
			if text := contentText(content); text != "" {
				return s.update(text, onDelta, onReplace)
			}
		}
	case path == "/message":
		if message, ok := operation["v"].(map[string]any); ok {
			return s.setMessage(message, onDelta, onReplace)
		}
	}
	return nil
}

func (s *partsSSEState) applyMetadataOperation(operation map[string]any) error {
	path, _ := operation["p"].(string)
	action, _ := operation["o"].(string)
	value := operation["v"]
	tokens := metadataTokens(path)
	if len(tokens) == 0 {
		if metadata, ok := value.(map[string]any); ok {
			if s.metadata == nil {
				s.metadata = map[string]any{}
			}
			if action == "replace" {
				s.metadata = metadata
			} else {
				for key, entry := range metadata {
					s.metadata[key] = entry
				}
			}
		}
	} else {
		if s.metadata == nil {
			s.metadata = map[string]any{}
		}
		if updated, ok := patchMetadataValue(s.metadata, tokens, action, value).(map[string]any); ok {
			s.metadata = updated
		}
	}
	if strings.Contains(path, "genui_components") || strings.Contains(path, "componentResults") {
		return s.emitGenUI()
	}
	return nil
}

func metadataTokens(path string) []string {
	rest := strings.TrimPrefix(path, "/message/metadata")
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		return nil
	}
	return strings.Split(rest, "/")
}

func patchMetadataValue(node any, tokens []string, action string, value any) any {
	if len(tokens) == 0 {
		return value
	}
	token := tokens[0]
	switch current := node.(type) {
	case map[string]any:
		if len(tokens) == 1 {
			applyMapPatch(current, token, action, value)
			return current
		}
		child, ok := current[token]
		if !ok || child == nil {
			if arrayToken(tokens[1]) {
				child = []any{}
			} else {
				child = map[string]any{}
			}
			current[token] = child
		}
		current[token] = patchMetadataValue(child, tokens[1:], action, value)
		return current
	case []any:
		index, err := strconv.Atoi(token)
		if err != nil || index < 0 {
			return current
		}
		for len(current) <= index {
			current = append(current, nil)
		}
		if len(tokens) == 1 {
			return applySlicePatch(current, index, action, value)
		}
		child := current[index]
		if child == nil {
			if arrayToken(tokens[1]) {
				child = []any{}
			} else {
				child = map[string]any{}
			}
			current[index] = child
		}
		current[index] = patchMetadataValue(child, tokens[1:], action, value)
		return current
	default:
		if arrayToken(token) {
			return patchMetadataValue([]any{}, tokens, action, value)
		}
		return patchMetadataValue(map[string]any{}, tokens, action, value)
	}
}

func applyMapPatch(target map[string]any, key, action string, value any) {
	switch action {
	case "append":
		if list, ok := target[key].([]any); ok {
			target[key] = append(list, appendValues(value)...)
		} else if target[key] != nil {
			target[key] = appendValues(value)
		} else {
			target[key] = value
		}
	case "add", "replace":
		target[key] = value
	case "remove":
		delete(target, key)
	case "truncate":
		if list, ok := target[key].([]any); ok {
			target[key] = truncateList(list, intValue(value))
		}
	}
}

func applySlicePatch(target []any, index int, action string, value any) []any {
	switch action {
	case "append":
		return append(target, appendValues(value)...)
	case "add":
		if index >= len(target) {
			return append(target, value)
		}
		target = append(target, nil)
		copy(target[index+1:], target[index:])
		target[index] = value
		return target
	case "replace":
		target[index] = value
	case "remove":
		return append(target[:index], target[index+1:]...)
	case "truncate":
		return truncateList(target, intValue(value))
	}
	return target
}

func appendValues(value any) []any {
	if list, ok := value.([]any); ok {
		return list
	}
	return []any{value}
}

func truncateList(list []any, limit int) []any {
	if limit < 0 {
		limit = 0
	}
	if limit > len(list) {
		limit = len(list)
	}
	return list[:limit]
}

func arrayToken(value string) bool {
	index, err := strconv.Atoi(value)
	return err == nil && index >= 0
}

func processPartsEvent(event map[string]any, state *partsSSEState, onDelta, onReplace func(string) error) error {
	if message, ok := event["message"].(map[string]any); ok {
		if err := state.setMessage(message, onDelta, onReplace); err != nil {
			return err
		}
	}
	if nested, ok := event["v"].(map[string]any); ok {
		if message, ok := nested["message"].(map[string]any); ok {
			if err := state.setMessage(message, onDelta, onReplace); err != nil {
				return err
			}
		}
		if _, ok := nested["p"]; ok {
			if err := state.applyPatch(nested, onDelta, onReplace); err != nil {
				return err
			}
		}
	}
	if operations, ok := event["v"].([]any); ok {
		for _, raw := range operations {
			operation, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if value, ok := operation["v"].(map[string]any); ok {
				if message, ok := value["message"].(map[string]any); ok {
					if err := state.setMessage(message, onDelta, onReplace); err != nil {
						return err
					}
				}
			}
			if _, ok := operation["p"]; ok {
				if err := state.applyPatch(operation, onDelta, onReplace); err != nil {
					return err
				}
			}
		}
	}
	if _, ok := event["p"]; ok {
		if err := state.applyPatch(event, onDelta, onReplace); err != nil {
			return err
		}
	}
	// 文本增量有时只带一个 v 字符串、没有 p 路径（事件只有 v 一个字段）。
	if len(event) == 1 {
		if value, ok := event["v"].(string); ok && state.currentID != "" {
			return state.update(state.current+value, onDelta, onReplace)
		}
	}
	return nil
}

// readConversationSSE 从 content.parts 读取助手正文。
// 文本通常通过 /message/content/parts/0 的 append/replace patch 下发，
// 这里维护状态并输出增量；完整的消息也可能直接出现在 add 事件里。
func readConversationSSE(reader io.Reader, onDelta, onReplace func(string) error, onGenUI func([]GenUIComponent) error) (string, string, string, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	conversationID := ""
	upstreamError := ""
	state := &partsSSEState{onGenUI: onGenUI}
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		value := strings.TrimPrefix(line, "data: ")
		value = strings.TrimPrefix(value, "data:")
		value = strings.TrimSpace(value)
		if value == "[DONE]" {
			break
		}
		var event map[string]any
		if json.Unmarshal([]byte(value), &event) != nil {
			continue
		}
		if errValue := event["error"]; errValue != nil {
			upstreamError = fmt.Sprint(errValue)
		} else if code := event["error_code"]; code != nil {
			upstreamError = fmt.Sprint(code)
		}
		if id, ok := event["conversation_id"].(string); ok && id != "" {
			conversationID = id
		}
		if nested, ok := event["v"].(map[string]any); ok {
			if id, ok := nested["conversation_id"].(string); ok && id != "" {
				conversationID = id
			}
		}
		if err := processPartsEvent(event, state, onDelta, onReplace); err != nil {
			return conversationID, state.bestID, state.best, err
		}
	}
	if err := scanner.Err(); err != nil {
		return conversationID, state.bestID, state.best, err
	}
	text := state.best
	if text == "" {
		text = state.current
	}
	messageID := state.bestID
	if messageID == "" {
		messageID = state.currentID
	}
	text = strings.TrimSpace(text)
	if text == "" && upstreamError != "" {
		return conversationID, messageID, text, fmt.Errorf("ChatGPT 返回错误：%s", upstreamError)
	}
	return conversationID, messageID, text, nil
}
