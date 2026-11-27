package chatgpt

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestReadConversationSSEStreamsGenUI(t *testing.T) {
	tag := `<Chart content={{"chartType":"bar","data":[{"month":"1月","sales":120}]}}/>`
	text := "前" + tag + "后"
	start := len([]rune("前"))
	end := start + len([]rune(tag))
	message := map[string]any{
		"id":       "m1",
		"author":   map[string]any{"role": "assistant"},
		"channel":  "final",
		"content":  map[string]any{"content_type": "text", "parts": []any{""}},
		"metadata": map[string]any{},
	}
	events := []any{
		map[string]any{"v": map[string]any{"message": message}},
		map[string]any{"v": []any{
			map[string]any{"o": "append", "p": "/message/content/parts/0", "v": text},
			map[string]any{"o": "append", "p": "/message/metadata/genui_components", "v": []any{
				map[string]any{"type": "charts_widget_v2", "start_index": start, "end_index": end},
			}},
		}},
	}
	var builder strings.Builder
	for _, event := range events {
		data, _ := json.Marshal(event)
		fmt.Fprintf(&builder, "data: %s\n\n", data)
	}
	builder.WriteString("data: [DONE]\n\n")

	var snapshots [][]GenUIComponent
	_, _, gotText, err := readConversationSSE(strings.NewReader(builder.String()), nil, nil, func(components []GenUIComponent) error {
		snapshots = append(snapshots, components)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotText != text {
		t.Fatalf("text = %q, want %q", gotText, text)
	}
	if len(snapshots) == 0 || len(snapshots[len(snapshots)-1]) != 1 {
		t.Fatalf("genui snapshots = %#v", snapshots)
	}
	component := snapshots[len(snapshots)-1][0]
	if component.Type != "charts_widget_v2" || component.StartIndex != start || component.EndIndex != end {
		t.Fatalf("component = %#v", component)
	}
	if component.Data["chartType"] != "bar" {
		t.Fatalf("chart data = %#v", component.Data)
	}
}
